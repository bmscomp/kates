package com.bmscomp.kates.disruption;

import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.ArrayList;
import java.util.List;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.event.Observes;
import jakarta.inject.Inject;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.fabric8.kubernetes.api.model.ObjectMeta;
import io.fabric8.kubernetes.api.model.networking.v1.NetworkPolicy;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.quarkus.runtime.StartupEvent;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import com.bmscomp.kates.chaos.KubernetesChaosProvider;
import com.bmscomp.kates.chaos.ScaleDownSnapshots;

/**
 * Cleans up faults abandoned by a previous process.
 *
 * <p>Disruptions inject real cluster state — NetworkPolicies that partition
 * brokers, KafkaNodePools and StatefulSets scaled below their intended size — and previously that
 * state was only ever undone by the in-process rollback path. If the pod was
 * killed mid-plan (deploy, OOM, node drain), the partition or the missing
 * brokers simply stayed, with nothing left running that knew to clean them up.
 * Tests already had {@code recoverOrphans}; disruptions had no equivalent.
 *
 * <p><b>Why the process start is the cutoff.</b> The chart pins Kates to one
 * replica and rolls it with {@code Recreate}, so no other pod can be running a
 * plan: every Kates fault created before this process started is an orphan,
 * however recent. {@code kates.chaos.orphan-recovery.min-age-sec} (default 0)
 * moves the cutoff that many seconds earlier, for a deployment that runs a
 * second Kates pod anyway.
 */
@ApplicationScoped
public class DisruptionOrphanReconciler {

    private static final Logger LOG = Logger.getLogger(DisruptionOrphanReconciler.class);

    @Inject
    KubernetesClient kubeClient;

    @Inject
    DisruptionReportRepository repository;

    @Inject
    ObjectMapper objectMapper;

    @ConfigProperty(name = "kates.chaos.orphan-recovery.enabled", defaultValue = "true")
    boolean enabled;

    @ConfigProperty(name = "kates.chaos.orphan-recovery.min-age-sec", defaultValue = "0")
    long minAgeSec;

    @ConfigProperty(name = "kates.chaos.kafka.namespace", defaultValue = "kafka")
    String kafkaNamespace;

    void onStart(@Observes StartupEvent event) {
        // Taken before the HTTP server accepts a request, so no plan this
        // process launches can have started earlier.
        Instant startedAt = Instant.now();
        if (!enabled) {
            LOG.debug("Disruption orphan recovery disabled");
            return;
        }
        // Off the startup thread, deliberately.
        //
        // Every call below goes to the Kubernetes API, and a StartupEvent
        // observer runs BEFORE the HTTP server accepts connections. With no
        // reachable API server — the native smoke test, a dev laptop, any
        // deployment without chaos RBAC — fabric8 does not fail fast: it
        // retries with exponential backoff (~100s at the default backoff
        // limit) per call. The catch below made failure harmless but not
        // fast, so the whole API stayed dark for the duration and the pod
        // failed its startup probe, with nothing in the log to say why:
        // boot-time categories are at WARN, so a blocked boot prints nothing
        // at all.
        //
        // Recovery is cleanup of faults made before this process started.
        // Nothing about it needs to happen before the first request is served.
        Thread.ofVirtual().name("disruption-orphan-recovery").start(() -> reconcile(startedAt));
    }

    private void reconcile(Instant startedAt) {
        try {
            int policies = reconcileNetworkPolicies(startedAt);
            ScaleDownSnapshots.Restored scaled = reconcileScaleDowns(startedAt);
            int reports = markInterruptedReports(startedAt);
            if (policies + scaled.total() + reports > 0) {
                LOG.warnf(
                        "Orphan recovery: removed %d NetworkPolicy(ies), restored %d KafkaNodePool(s) and %d"
                                + " StatefulSet(s), marked %d report(s) INTERRUPTED",
                        policies, scaled.nodePools(), scaled.statefulSets(), reports);
            }
        } catch (Exception e) {
            // Never block startup on cleanup — the API must come up either way.
            LOG.warn("Disruption orphan recovery failed", e);
        }
    }

    /**
     * Deletes the Kates-managed NetworkPolicies (network partitions) left from
     * before this process started, in every namespace, as cleanup does: a
     * playbook can isolate pods outside the Kafka namespace.
     */
    int reconcileNetworkPolicies(Instant startedAt) {
        long cutoff = cutoff(startedAt);
        List<NetworkPolicy> policies;
        try {
            policies = kubeClient
                    .network()
                    .networkPolicies()
                    .inAnyNamespace()
                    .withLabel("managed-by", "kates")
                    .list()
                    .getItems();
        } catch (Exception e) {
            // Concise on purpose: running without a reachable Kubernetes API
            // (dev, unit tests) is normal and must not dump a stack trace on
            // every boot.
            LOG.warnf("Could not reconcile orphaned NetworkPolicies: %s", e.getMessage());
            return 0;
        }
        int removed = 0;
        for (NetworkPolicy policy : policies) {
            if (!isOlderThan(policy.getMetadata().getCreationTimestamp(), cutoff)) {
                continue;
            }
            String namespace = policy.getMetadata().getNamespace();
            String name = policy.getMetadata().getName();
            // One try per policy: a namespace that refuses the delete must not
            // stop recovery in the others.
            try {
                kubeClient
                        .network()
                        .networkPolicies()
                        .inNamespace(namespace)
                        .withName(name)
                        .delete();
                LOG.warnf("Orphan recovery: deleted abandoned NetworkPolicy %s/%s", namespace, name);
                removed++;
            } catch (Exception e) {
                LOG.warnf("Could not delete orphaned NetworkPolicy %s/%s: %s", namespace, name, e.getMessage());
            }
        }
        return removed;
    }

    /**
     * Restores KafkaNodePools and StatefulSets still carrying a scale-down
     * snapshot. The snapshot annotation written at SCALE_DOWN time is the
     * durable record of the original size — it survives the pod that created
     * it, which is exactly what makes recovery possible here.
     */
    ScaleDownSnapshots.Restored reconcileScaleDowns(Instant startedAt) {
        long cutoff = cutoff(startedAt);
        return ScaleDownSnapshots.restore(kubeClient, kafkaNamespace, meta -> isScaleDownOlderThan(meta, cutoff));
    }

    private long cutoff(Instant startedAt) {
        // creationTimestamp has whole seconds: a policy made in this process's
        // first second can be stamped before startedAt itself.
        return startedAt.truncatedTo(ChronoUnit.SECONDS).toEpochMilli() - minAgeSec * 1000L;
    }

    /**
     * A RUNNING report row whose process is gone can never complete — leaving it
     * RUNNING forever misreports an in-flight disruption that no longer exists.
     *
     * <p>Only reports created before this process started are marked: the API
     * is already serving while this runs, and a plan launched since then is
     * alive. Like the disruption lease, this assumes one replica. A plan
     * another replica started earlier and is still running is marked too, and
     * gets its real outcome when it ends, because the launcher's final save
     * replaces the row whatever its status.
     *
     * <p>The status changes in the row, which the list reads, and in the stored
     * report, which a GET returns, and only while the row still says RUNNING.
     * This never worked before: the query ran on this thread with neither a
     * transaction nor a request context and threw, and the save it never
     * reached was an insert that would have failed on the primary key.
     */
    int markInterruptedReports(Instant startedBefore) {
        List<DisruptionReportEntity> running;
        try {
            running = repository.findByStatusCreatedBefore("RUNNING", startedBefore);
        } catch (Exception e) {
            LOG.warn("Could not read the disruption reports left RUNNING", e);
            return 0;
        }
        int marked = 0;
        for (DisruptionReportEntity entity : running) {
            try {
                if (repository.saveIfStatus(interrupted(entity), "RUNNING")) {
                    marked++;
                }
            } catch (Exception e) {
                LOG.warnf(e, "Could not mark disruption report %s INTERRUPTED", entity.getId());
            }
        }
        return marked;
    }

    private DisruptionReportEntity interrupted(DisruptionReportEntity entity) throws JsonProcessingException {
        DisruptionReport report = DisruptionPersistence.readReport(entity, objectMapper);
        if (report == null) {
            report = new DisruptionReport();
            report.setPlanName(entity.getPlanName());
        }
        report.setStatus("INTERRUPTED");
        List<String> warnings =
                new ArrayList<>(report.getValidationWarnings() != null ? report.getValidationWarnings() : List.of());
        warnings.add("Interrupted: the Kates process running this plan stopped before the plan finished,"
                + " so its outcome was not recorded");
        report.setValidationWarnings(warnings);
        return DisruptionPersistence.toEntity(entity.getId(), report, objectMapper);
    }

    private boolean isScaleDownOlderThan(ObjectMeta meta, long cutoff) {
        String stamp = meta.getAnnotations().get(KubernetesChaosProvider.SCALED_DOWN_AT_ANNOTATION);
        if (stamp != null) {
            try {
                return Long.parseLong(stamp) < cutoff;
            } catch (NumberFormatException ignored) {
                // fall through to the resource timestamp
            }
        }
        // Snapshot written before the timestamp annotation existed: fall back to
        // the object's own age, which is at least as old as the scale-down.
        return isOlderThan(meta.getCreationTimestamp(), cutoff);
    }

    private boolean isOlderThan(String k8sTimestamp, long cutoffMs) {
        if (k8sTimestamp == null) {
            return false; // Unknown age — leave it alone rather than risk a live fault.
        }
        try {
            return java.time.Instant.parse(k8sTimestamp).toEpochMilli() < cutoffMs;
        } catch (Exception e) {
            return false;
        }
    }
}
