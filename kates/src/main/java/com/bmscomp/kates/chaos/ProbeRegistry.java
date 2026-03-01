package com.bmscomp.kates.chaos;

import java.util.List;
import java.util.Map;
import static java.util.Map.entry;

/**
 * Maps {@link DisruptionType} to sensible default probes.
 * When a resilience test does not specify explicit probes,
 * the registry provides Kafka-appropriate defaults.
 */
public final class ProbeRegistry {

    private ProbeRegistry() {}

    private static final Map<DisruptionType, List<ProbeSpec>> DEFAULT_PROBES = Map.ofEntries(
            entry(DisruptionType.POD_KILL,           List.of(KafkaProbes.isrHealth(), KafkaProbes.clusterReady())),
            entry(DisruptionType.POD_DELETE,          List.of(KafkaProbes.isrHealth(), KafkaProbes.clusterReady())),
            entry(DisruptionType.CPU_STRESS,          List.of(KafkaProbes.producerThroughput(), KafkaProbes.clusterReady())),
            entry(DisruptionType.MEMORY_STRESS,       List.of(KafkaProbes.producerThroughput(), KafkaProbes.clusterReady())),
            entry(DisruptionType.IO_STRESS,           List.of(KafkaProbes.isrHealth(), KafkaProbes.producerThroughput())),
            entry(DisruptionType.DNS_ERROR,           List.of(KafkaProbes.clusterReady(), KafkaProbes.consumerLatency())),
            entry(DisruptionType.DISK_FILL,           List.of(KafkaProbes.isrHealth(), KafkaProbes.partitionAvailability())),
            entry(DisruptionType.NETWORK_PARTITION,   List.of(KafkaProbes.minIsr(), KafkaProbes.consumerLatency())),
            entry(DisruptionType.NETWORK_LATENCY,     List.of(KafkaProbes.producerThroughput(), KafkaProbes.consumerLatency())),
            entry(DisruptionType.NODE_DRAIN,          List.of(KafkaProbes.isrHealth(), KafkaProbes.partitionAvailability())),
            entry(DisruptionType.ROLLING_RESTART,     List.of(KafkaProbes.clusterReady(), KafkaProbes.isrHealth())),
            entry(DisruptionType.LEADER_ELECTION,     List.of(KafkaProbes.clusterReady(), KafkaProbes.producerThroughput())),
            entry(DisruptionType.SCALE_DOWN,          List.of(KafkaProbes.isrHealth(), KafkaProbes.partitionAvailability()))
    );

    /**
     * Returns the default probes for a given disruption type.
     * Falls back to ISR health + cluster ready if no mapping exists.
     */
    public static List<ProbeSpec> defaultProbesFor(DisruptionType type) {
        return DEFAULT_PROBES.getOrDefault(type, List.of(KafkaProbes.isrHealth(), KafkaProbes.clusterReady()));
    }

    /**
     * Returns probes for a FaultSpec: uses explicit probes if set, otherwise auto-attaches defaults.
     */
    public static List<ProbeSpec> resolve(FaultSpec spec) {
        if (spec.probes() != null && !spec.probes().isEmpty()) {
            return spec.probes();
        }
        if (spec.disruptionType() != null) {
            return defaultProbesFor(spec.disruptionType());
        }
        return List.of(KafkaProbes.isrHealth(), KafkaProbes.clusterReady());
    }
}
