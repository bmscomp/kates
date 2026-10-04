package com.bmscomp.kates.disruption;

import java.util.Optional;
import java.util.concurrent.ConcurrentHashMap;
import jakarta.enterprise.context.ApplicationScoped;

import org.eclipse.microprofile.config.inject.ConfigProperty;

/**
 * Serialises disruption plans per target cluster: at most one plan may be
 * running against a given namespace/cluster at a time.
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
 */
@ApplicationScoped
public class DisruptionConcurrencyGuard {

    private final ConcurrentHashMap<String, String> activeByTarget = new ConcurrentHashMap<>();

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
        return activeByTarget.putIfAbsent(target, token) == null;
    }

    /** Releases the lease, but only if {@code token} still holds it. */
    public void release(String target, String token) {
        activeByTarget.remove(target, token);
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
