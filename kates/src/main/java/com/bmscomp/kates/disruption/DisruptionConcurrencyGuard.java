package com.bmscomp.kates.disruption;

import java.util.LinkedHashSet;
import java.util.Optional;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.fabric8.kubernetes.api.model.ObjectMeta;
import io.fabric8.kubernetes.client.KubernetesClient;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import com.bmscomp.kates.chaos.KubernetesChaosProvider;
import com.bmscomp.kates.chaos.ScaleDownSnapshots;

/**
 * Serialises plans, compound runs and resilience faults per target cluster:
 * at most one of them may be running against a given namespace/cluster.
 *
 * <p><b>Why.</b> Plans are executed asynchronously, so nothing stopped two of
 * them overlapping. Rollback state is stored on the target itself — the
 * {@code kates.io/original-replicas} annotation is stamped once, on the first
 * scale-down, and cleared on rollback. With two concurrent plans, plan A's
 * rollback clears the snapshot plan B still depends on, and B restores the
 * wrong replica count (or none at all), leaving the StatefulSet permanently
 * under-scaled. Chaos results are also meaningless when a second plan is
 * perturbing the same brokers.
 *
 * <p>Holders are identified by an opaque token rather than the plan name, so
 * two runs of the same plan cannot release each other's lease.
 *
 * <p>Releasing the lease ends the run, so it gives back every broker a
 * SCALE_DOWN of the run removed, before another run may start. A plan only
 * restored one when a step failed its recovery check, and a compound or
 * resilience run never did: the broker stayed removed until the next Kates
 * API restart, whose orphan recovery then put it back, weeks later perhaps,
 * over whatever size someone had set since.
 */
@ApplicationScoped
public class DisruptionConcurrencyGuard {

    private static final Logger LOG = Logger.getLogger(DisruptionConcurrencyGuard.class);

    private final ConcurrentHashMap<String, String> activeByTarget = new ConcurrentHashMap<>();

    /** When each holder took the lease, in epoch millis, by token. */
    private final ConcurrentHashMap<String, Long> acquiredAt = new ConcurrentHashMap<>();

    /** Null in unit tests that build the guard with new: nothing is restored then. */
    @Inject
    KubernetesClient kubeClient;

    @ConfigProperty(name = "kates.chaos.kafka.namespace", defaultValue = "kafka")
    String kafkaNamespace;

    @ConfigProperty(name = "kates.chaos.kafka.cluster", defaultValue = "krafter")
    String kafkaCluster;

    /** The target every plan currently runs against: {@code namespace/cluster}. */
    public String currentTarget() {
        return kafkaNamespace + "/" + kafkaCluster;
    }

    /**
     * Takes the lease for {@code target}, or returns false if another plan holds it.
     *
     * @param token opaque holder identity; the same value must be passed to
     *     {@link #release(String, String)}.
     */
    public boolean tryAcquire(String target, String token) {
        long now = System.currentTimeMillis();
        if (activeByTarget.putIfAbsent(target, token) != null) {
            return false;
        }
        acquiredAt.put(token, now);
        return true;
    }

    /**
     * Releases the lease, but only if {@code token} still holds it, after
     * giving back what the holder's SCALE_DOWNs removed.
     */
    public void release(String target, String token) {
        Long since = acquiredAt.remove(token);
        if (since != null && token.equals(activeByTarget.get(target))) {
            restoreScaleDownsSince(since);
        }
        activeByTarget.remove(target, token);
    }

    /**
     * Every StatefulSet and KafkaNodePool scaled down since {@code sinceMs}, in
     * the two namespaces a step may target: the lease kept any other run from
     * scaling one down meanwhile. A failed restore is logged and its snapshot
     * kept, for orphan recovery at the next start.
     */
    private void restoreScaleDownsSince(long sinceMs) {
        if (kubeClient == null) {
            return;
        }
        Set<String> namespaces = new LinkedHashSet<>();
        namespaces.add(kafkaNamespace);
        if (kubeClient.getNamespace() != null) {
            namespaces.add(kubeClient.getNamespace());
        }
        for (String namespace : namespaces) {
            ScaleDownSnapshots.Restored restored =
                    ScaleDownSnapshots.restore(kubeClient, namespace, meta -> scaledDownAt(meta) >= sinceMs);
            if (restored.total() > 0) {
                LOG.infof(
                        "The run ended: gave back the replicas its SCALE_DOWN took from %d resource(s) in %s",
                        restored.total(), namespace);
            }
        }
    }

    private static long scaledDownAt(ObjectMeta meta) {
        String stamp = meta.getAnnotations() != null
                ? meta.getAnnotations().get(KubernetesChaosProvider.SCALED_DOWN_AT_ANNOTATION)
                : null;
        try {
            return stamp != null ? Long.parseLong(stamp) : Long.MIN_VALUE;
        } catch (NumberFormatException e) {
            return Long.MIN_VALUE;
        }
    }

    /** The plan token currently running against {@code target}, if any. */
    public Optional<String> activeToken(String target) {
        return Optional.ofNullable(activeByTarget.get(target));
    }

    /** True when a plan is running against {@code target}. */
    public boolean isBusy(String target) {
        return activeByTarget.containsKey(target);
    }
}
