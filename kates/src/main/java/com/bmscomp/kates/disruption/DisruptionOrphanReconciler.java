package com.bmscomp.kates.disruption;

import java.util.List;
import java.util.Map;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.event.Observes;
import jakarta.inject.Inject;

import io.fabric8.kubernetes.api.model.apps.StatefulSet;
import io.fabric8.kubernetes.api.model.apps.StatefulSetBuilder;
import io.fabric8.kubernetes.api.model.networking.v1.NetworkPolicy;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.quarkus.runtime.StartupEvent;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import com.bmscomp.kates.chaos.KubernetesChaosProvider;

/**
 * Cleans up faults abandoned by a previous process.
 *
 * <p>Disruptions inject real cluster state — NetworkPolicies that partition
 * brokers, StatefulSets scaled below their intended size — and previously that
 * state was only ever undone by the in-process rollback path. If the pod was
 * killed mid-plan (deploy, OOM, node drain), the partition or the missing
 * brokers simply stayed, with nothing left running that knew to clean them up.
 * Tests already had {@code recoverOrphans}; disruptions had no equivalent.
 *
 * <p><b>Why age matters.</b> Kates may run more than one replica, so a fault
 * seen at startup is not necessarily abandoned — another pod could be running
 * that plan right now. Only faults older than
 * {@code kates.chaos.orphan-recovery.min-age-sec} (default 15m, comfortably past
 * the per-step recovery timeout) are treated as orphans, so live chaos is never
 * ripped out from under a peer.
 */
@ApplicationScoped
public class DisruptionOrphanReconciler {

    private static final Logger LOG = Logger.getLogger(DisruptionOrphanReconciler.class);

    @Inject
    KubernetesClient kubeClient;

    @Inject
    DisruptionReportRepository repository;

    @ConfigProperty(name = "kates.chaos.orphan-recovery.enabled", defaultValue = "true")
    boolean enabled;

    @ConfigProperty(name = "kates.chaos.orphan-recovery.min-age-sec", defaultValue = "900")
    long minAgeSec;

    @ConfigProperty(name = "kates.chaos.kafka.namespace", defaultValue = "kafka")
    String kafkaNamespace;

    void onStart(@Observes StartupEvent event) {
        if (!enabled) {
            LOG.debug("Disruption orphan recovery disabled");
            return;
        }
        try {
            int policies = reconcileNetworkPolicies();
            int scaled = reconcileScaledDownStatefulSets();
            int reports = markInterruptedReports();
            if (policies + scaled + reports > 0) {
                LOG.warnf(
                        "Orphan recovery: removed %d NetworkPolicy(ies), restored %d StatefulSet(s), "
                                + "marked %d report(s) INTERRUPTED",
                        policies, scaled, reports);
            }
        } catch (Exception e) {
            // Never block startup on cleanup — the API must come up either way.
            LOG.warn("Disruption orphan recovery failed", e);
        }
    }

    /** Deletes stale Kates-managed NetworkPolicies (network partitions). */
    private int reconcileNetworkPolicies() {
        long cutoff = System.currentTimeMillis() - minAgeSec * 1000L;
        int removed = 0;
        try {
            List<NetworkPolicy> policies = kubeClient
                    .network()
                    .networkPolicies()
                    .inNamespace(kafkaNamespace)
                    .withLabel("managed-by", "kates")
                    .list()
                    .getItems();
            for (NetworkPolicy policy : policies) {
                if (!isOlderThan(policy.getMetadata().getCreationTimestamp(), cutoff)) {
                    continue;
                }
                kubeClient
                        .network()
                        .networkPolicies()
                        .inNamespace(kafkaNamespace)
                        .withName(policy.getMetadata().getName())
                        .delete();
                LOG.warnf(
                        "Orphan recovery: deleted abandoned NetworkPolicy %s",
                        policy.getMetadata().getName());
                removed++;
            }
        } catch (Exception e) {
            // Concise on purpose: running without a reachable Kubernetes API
            // (dev, unit tests) is normal and must not dump a stack trace on
            // every boot.
            LOG.warnf("Could not reconcile orphaned NetworkPolicies: %s", e.getMessage());
        }
        return removed;
    }

    /**
     * Restores StatefulSets still carrying a scale-down snapshot. The snapshot
     * annotation written at SCALE_DOWN time is the durable record of the
     * original size — it survives the pod that created it, which is exactly what
     * makes recovery possible here.
     */
    private int reconcileScaledDownStatefulSets() {
        long cutoff = System.currentTimeMillis() - minAgeSec * 1000L;
        int restored = 0;
        try {
            List<StatefulSet> sets = kubeClient
                    .apps()
                    .statefulSets()
                    .inNamespace(kafkaNamespace)
                    .list()
                    .getItems();
            for (StatefulSet ss : sets) {
                Map<String, String> annotations = ss.getMetadata().getAnnotations();
                if (annotations == null) {
                    continue;
                }
                String snapshot = annotations.get(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION);
                if (snapshot == null) {
                    continue;
                }
                if (!isScaleDownOlderThan(annotations, ss, cutoff)) {
                    continue;
                }

                String name = ss.getMetadata().getName();
                int original;
                try {
                    original = Integer.parseInt(snapshot);
                } catch (NumberFormatException e) {
                    LOG.warnf("Orphan recovery: unparseable replica snapshot '%s' on %s", snapshot, name);
                    continue;
                }
                int current = ss.getSpec().getReplicas() != null ? ss.getSpec().getReplicas() : original;
                if (current < original) {
                    LOG.warnf("Orphan recovery: restoring %s from %d → %d", name, current, original);
                    kubeClient
                            .apps()
                            .statefulSets()
                            .inNamespace(kafkaNamespace)
                            .withName(name)
                            .scale(original);
                    restored++;
                }
                clearSnapshot(name);
            }
        } catch (Exception e) {
            LOG.warnf("Could not reconcile orphaned scale-downs: %s", e.getMessage());
        }
        return restored;
    }

    private void clearSnapshot(String name) {
        try {
            kubeClient
                    .apps()
                    .statefulSets()
                    .inNamespace(kafkaNamespace)
                    .withName(name)
                    .edit(s -> new StatefulSetBuilder(s)
                            .editMetadata()
                            .removeFromAnnotations(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION)
                            .removeFromAnnotations(KubernetesChaosProvider.SCALED_DOWN_AT_ANNOTATION)
                            .endMetadata()
                            .build());
        } catch (Exception e) {
            LOG.debugf("Could not clear scale-down snapshot on %s: %s", name, e.getMessage());
        }
    }

    /**
     * A RUNNING report row whose process is gone can never complete — leaving it
     * RUNNING forever misreports an in-flight disruption that no longer exists.
     */
    private int markInterruptedReports() {
        int marked = 0;
        try {
            for (DisruptionReportEntity entity : repository.findByStatus("RUNNING")) {
                entity.setStatus("INTERRUPTED");
                repository.save(entity);
                marked++;
            }
        } catch (Exception e) {
            LOG.warn("Could not mark interrupted disruption reports", e);
        }
        return marked;
    }

    private boolean isScaleDownOlderThan(Map<String, String> annotations, StatefulSet ss, long cutoff) {
        String stamp = annotations.get(KubernetesChaosProvider.SCALED_DOWN_AT_ANNOTATION);
        if (stamp != null) {
            try {
                return Long.parseLong(stamp) < cutoff;
            } catch (NumberFormatException ignored) {
                // fall through to the resource timestamp
            }
        }
        // Snapshot written before the timestamp annotation existed: fall back to
        // the object's own age, which is at least as old as the scale-down.
        return isOlderThan(ss.getMetadata().getCreationTimestamp(), cutoff);
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
