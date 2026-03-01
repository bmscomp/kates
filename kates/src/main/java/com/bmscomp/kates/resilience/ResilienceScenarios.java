package com.bmscomp.kates.resilience;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

import com.bmscomp.kates.chaos.*;

/**
 * Pre-built resilience test scenarios for common Kafka failure modes.
 * Each scenario combines a disruption type with appropriate probes
 * and sensible default parameters.
 */
public final class ResilienceScenarios {

    private ResilienceScenarios() {}

    public record Scenario(
            String id,
            String name,
            String description,
            DisruptionType disruptionType,
            List<ProbeSpec> probes,
            int chaosDurationSec,
            int steadyStateSec,
            int maxRecoveryWaitSec) {}

    private static final List<Scenario> SCENARIOS = List.of(
            new Scenario(
                    "broker-crash",
                    "Broker Crash",
                    "Kill a random broker pod and verify cluster recovers with full ISR",
                    DisruptionType.POD_DELETE,
                    List.of(KafkaProbes.isrHealth(), KafkaProbes.clusterReady()),
                    30, 30, 120),
            new Scenario(
                    "memory-pressure",
                    "Memory Pressure",
                    "Consume 500MB on a broker pod to test OOM resilience",
                    DisruptionType.MEMORY_STRESS,
                    List.of(KafkaProbes.producerThroughput(), KafkaProbes.clusterReady()),
                    60, 30, 90),
            new Scenario(
                    "disk-saturation",
                    "Disk Saturation",
                    "Inject disk I/O pressure on broker storage (50% FS utilization)",
                    DisruptionType.IO_STRESS,
                    List.of(KafkaProbes.isrHealth(), KafkaProbes.producerThroughput()),
                    60, 30, 120),
            new Scenario(
                    "dns-failure",
                    "DNS Failure",
                    "Inject DNS resolution failures on broker pods",
                    DisruptionType.DNS_ERROR,
                    List.of(KafkaProbes.clusterReady(), KafkaProbes.consumerLatency()),
                    30, 30, 90),
            new Scenario(
                    "node-maintenance",
                    "Node Maintenance",
                    "Drain the Kubernetes node hosting a broker pod",
                    DisruptionType.NODE_DRAIN,
                    List.of(KafkaProbes.isrHealth(), KafkaProbes.partitionAvailability()),
                    60, 30, 180),
            new Scenario(
                    "network-split",
                    "Network Split",
                    "Isolate a broker from peers via NetworkPolicy",
                    DisruptionType.NETWORK_PARTITION,
                    List.of(KafkaProbes.minIsr(), KafkaProbes.consumerLatency()),
                    60, 30, 120),
            new Scenario(
                    "cpu-exhaustion",
                    "CPU Exhaustion",
                    "Exhaust CPU on a broker container",
                    DisruptionType.CPU_STRESS,
                    List.of(KafkaProbes.producerThroughput(), KafkaProbes.clusterReady()),
                    60, 30, 90)
    );

    /**
     * Returns all pre-built scenarios as a list of summary maps.
     */
    public static List<Map<String, Object>> listAll() {
        return SCENARIOS.stream()
                .map(s -> {
                    Map<String, Object> m = new LinkedHashMap<>();
                    m.put("id", s.id());
                    m.put("name", s.name());
                    m.put("description", s.description());
                    m.put("disruptionType", s.disruptionType().name());
                    m.put("probeCount", s.probes().size());
                    m.put("chaosDurationSec", s.chaosDurationSec());
                    m.put("maxRecoveryWaitSec", s.maxRecoveryWaitSec());
                    return m;
                })
                .toList();
    }

    /**
     * Find a scenario by ID and build a ResilienceTestRequest with optional overrides.
     */
    public static Scenario findById(String id) {
        return SCENARIOS.stream()
                .filter(s -> s.id().equals(id))
                .findFirst()
                .orElse(null);
    }

    /**
     * Build a FaultSpec from a scenario with optional target overrides.
     */
    public static FaultSpec buildFaultSpec(Scenario scenario, Map<String, Object> overrides) {
        String targetPod = overrides != null && overrides.containsKey("targetPod")
                ? overrides.get("targetPod").toString()
                : "";

        int durationSec = overrides != null && overrides.containsKey("chaosDurationSec")
                ? ((Number) overrides.get("chaosDurationSec")).intValue()
                : scenario.chaosDurationSec();

        return FaultSpec.builder(scenario.id())
                .disruptionType(scenario.disruptionType())
                .chaosDurationSec(durationSec)
                .targetPod(targetPod)
                .probes(scenario.probes())
                .build();
    }
}
