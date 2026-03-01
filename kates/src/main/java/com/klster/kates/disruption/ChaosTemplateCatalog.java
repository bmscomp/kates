package com.klster.kates.disruption;

import java.util.List;
import java.util.Map;
import jakarta.enterprise.context.ApplicationScoped;

import com.klster.kates.chaos.DisruptionType;
import com.klster.kates.chaos.FaultSpec;

/**
 * Pre-built chaos experiment templates with sensible defaults.
 * Each template produces a ready-to-execute DisruptionPlan
 * targeting common Kafka failure scenarios.
 */
@ApplicationScoped
public class ChaosTemplateCatalog {

    public record TemplateInfo(
            String id, String name, String description, String category, String severity, int estimatedDurationSec) {}

    public List<TemplateInfo> listTemplates() {
        return List.of(
                new TemplateInfo(
                        "broker-kill-recovery",
                        "Broker Kill & Recovery",
                        "Kills a single Kafka broker pod and measures ISR shrink, leader election time, and full recovery",
                        "availability",
                        "HIGH",
                        180),
                new TemplateInfo(
                        "network-partition-split-brain",
                        "Network Partition (Split Brain)",
                        "Creates a network partition isolating one broker, simulating split-brain. Verifies ISR integrity and consumer rebalancing",
                        "network",
                        "CRITICAL",
                        240),
                new TemplateInfo(
                        "disk-pressure",
                        "Disk Pressure (Storage Stress)",
                        "Fills broker disk to configurable threshold, testing log segment rotation and retention policy under pressure",
                        "storage",
                        "HIGH",
                        300),
                new TemplateInfo(
                        "rolling-restart-zero-downtime",
                        "Rolling Restart (Zero-Downtime)",
                        "Performs a controlled rolling restart of all brokers, verifying zero message loss and maintained throughput",
                        "upgrade",
                        "MEDIUM",
                        600),
                new TemplateInfo(
                        "leader-election-storm",
                        "Leader Election Storm",
                        "Triggers rapid leader elections on a hot partition to measure election latency and producer retry behavior",
                        "availability",
                        "HIGH",
                        120),
                new TemplateInfo(
                        "consumer-isolation",
                        "Consumer Group Isolation",
                        "Isolates consumer group members via network policy, testing rebalancing speed and offset commit consistency",
                        "network",
                        "MEDIUM",
                        180),
                new TemplateInfo(
                        "cascading-broker-failure",
                        "Cascading Broker Failure",
                        "Sequentially kills multiple brokers with observation windows, testing cluster resilience under progressive failure",
                        "availability",
                        "CRITICAL",
                        480),
                new TemplateInfo(
                        "cpu-saturation",
                        "CPU Saturation Attack",
                        "Stresses broker CPU to measure throughput degradation curve and request queue depth under load",
                        "performance",
                        "MEDIUM",
                        120));
    }

    public DisruptionPlan buildPlan(String templateId) {
        return buildPlan(templateId, Map.of());
    }

    public DisruptionPlan buildPlan(String templateId, Map<String, Object> overrides) {
        return switch (templateId) {
            case "broker-kill-recovery" -> brokerKillRecovery(overrides);
            case "network-partition-split-brain" -> networkPartitionSplitBrain(overrides);
            case "disk-pressure" -> diskPressure(overrides);
            case "rolling-restart-zero-downtime" -> rollingRestartZeroDowntime(overrides);
            case "leader-election-storm" -> leaderElectionStorm(overrides);
            case "consumer-isolation" -> consumerIsolation(overrides);
            case "cascading-broker-failure" -> cascadingBrokerFailure(overrides);
            case "cpu-saturation" -> cpuSaturation(overrides);
            default -> throw new IllegalArgumentException("Unknown template: " + templateId);
        };
    }

    private DisruptionPlan brokerKillRecovery(Map<String, Object> ov) {
        int brokerId = intOr(ov, "brokerId", 0);
        int chaosDuration = intOr(ov, "chaosDurationSec", 30);

        DisruptionPlan plan =
                basePlan("Broker Kill & Recovery", "Kill broker-" + brokerId + " and measure recovery time");
        plan.setMaxAffectedBrokers(1);

        plan.setSteps(List.of(new DisruptionPlan.DisruptionStep(
                "kill-broker-" + brokerId,
                FaultSpec.builder("broker-kill-" + brokerId)
                        .disruptionType(DisruptionType.POD_KILL)
                        .targetBrokerId(brokerId)
                        .chaosDurationSec(chaosDuration)
                        .gracePeriodSec(0)
                        .build(),
                30,
                60,
                true)));
        return plan;
    }

    private DisruptionPlan networkPartitionSplitBrain(Map<String, Object> ov) {
        int brokerId = intOr(ov, "brokerId", 0);
        int duration = intOr(ov, "chaosDurationSec", 60);

        DisruptionPlan plan =
                basePlan("Network Partition (Split Brain)", "Isolate broker-" + brokerId + " via NetworkPolicy");
        plan.setMaxAffectedBrokers(1);

        plan.setSteps(List.of(new DisruptionPlan.DisruptionStep(
                "partition-broker-" + brokerId,
                FaultSpec.builder("net-partition-" + brokerId)
                        .disruptionType(DisruptionType.NETWORK_PARTITION)
                        .targetBrokerId(brokerId)
                        .chaosDurationSec(duration)
                        .build(),
                30,
                90,
                true)));
        return plan;
    }

    private DisruptionPlan diskPressure(Map<String, Object> ov) {
        int brokerId = intOr(ov, "brokerId", 0);
        int fillPct = intOr(ov, "fillPercentage", 85);

        DisruptionPlan plan = basePlan("Disk Pressure", "Fill broker-" + brokerId + " disk to " + fillPct + "%");
        plan.setMaxAffectedBrokers(1);

        plan.setSteps(List.of(new DisruptionPlan.DisruptionStep(
                "disk-fill-" + brokerId,
                FaultSpec.builder("disk-pressure-" + brokerId)
                        .disruptionType(DisruptionType.DISK_FILL)
                        .targetBrokerId(brokerId)
                        .fillPercentage(fillPct)
                        .chaosDurationSec(60)
                        .build(),
                30,
                120,
                true)));
        return plan;
    }

    private DisruptionPlan rollingRestartZeroDowntime(Map<String, Object> ov) {
        DisruptionPlan plan =
                basePlan("Rolling Restart (Zero-Downtime)", "Sequentially restart all brokers with grace period");
        plan.setMaxAffectedBrokers(-1);

        plan.setSteps(List.of(new DisruptionPlan.DisruptionStep(
                "rolling-restart",
                FaultSpec.builder("rolling-restart")
                        .disruptionType(DisruptionType.ROLLING_RESTART)
                        .gracePeriodSec(intOr(ov, "gracePeriodSec", 60))
                        .chaosDurationSec(0)
                        .build(),
                60,
                120,
                true)));
        return plan;
    }

    private DisruptionPlan leaderElectionStorm(Map<String, Object> ov) {
        String topic = stringOr(ov, "targetTopic", "");
        int partition = intOr(ov, "targetPartition", 0);

        DisruptionPlan plan =
                basePlan("Leader Election Storm", "Trigger rapid leader elections on partition " + partition);
        plan.setMaxAffectedBrokers(1);

        plan.setSteps(List.of(new DisruptionPlan.DisruptionStep(
                "leader-election",
                FaultSpec.builder("leader-storm")
                        .disruptionType(DisruptionType.LEADER_ELECTION)
                        .targetTopic(topic)
                        .targetPartition(partition)
                        .chaosDurationSec(intOr(ov, "chaosDurationSec", 20))
                        .build(),
                10,
                30,
                true)));
        return plan;
    }

    private DisruptionPlan consumerIsolation(Map<String, Object> ov) {
        int brokerId = intOr(ov, "brokerId", 0);

        DisruptionPlan plan = basePlan("Consumer Group Isolation", "Isolate consumers connected to broker-" + brokerId);
        plan.setMaxAffectedBrokers(1);

        plan.setSteps(List.of(new DisruptionPlan.DisruptionStep(
                "isolate-consumers",
                FaultSpec.builder("consumer-isolation-" + brokerId)
                        .disruptionType(DisruptionType.NETWORK_PARTITION)
                        .targetBrokerId(brokerId)
                        .chaosDurationSec(intOr(ov, "chaosDurationSec", 45))
                        .build(),
                20,
                60,
                true)));
        return plan;
    }

    private DisruptionPlan cascadingBrokerFailure(Map<String, Object> ov) {
        DisruptionPlan plan = basePlan("Cascading Broker Failure", "Kill brokers 0 and 1 sequentially");
        plan.setMaxAffectedBrokers(2);

        plan.setSteps(List.of(
                new DisruptionPlan.DisruptionStep(
                        "kill-broker-0",
                        FaultSpec.builder("cascade-kill-0")
                                .disruptionType(DisruptionType.POD_KILL)
                                .targetBrokerId(0)
                                .chaosDurationSec(30)
                                .gracePeriodSec(0)
                                .build(),
                        30,
                        60,
                        true),
                new DisruptionPlan.DisruptionStep(
                        "kill-broker-1",
                        FaultSpec.builder("cascade-kill-1")
                                .disruptionType(DisruptionType.POD_KILL)
                                .targetBrokerId(1)
                                .chaosDurationSec(30)
                                .gracePeriodSec(0)
                                .build(),
                        30,
                        120,
                        true)));
        return plan;
    }

    private DisruptionPlan cpuSaturation(Map<String, Object> ov) {
        int brokerId = intOr(ov, "brokerId", 0);
        int cores = intOr(ov, "cpuCores", 2);

        DisruptionPlan plan =
                basePlan("CPU Saturation Attack", "Stress broker-" + brokerId + " CPU with " + cores + " cores");
        plan.setMaxAffectedBrokers(1);

        plan.setSteps(List.of(new DisruptionPlan.DisruptionStep(
                "cpu-stress-" + brokerId,
                FaultSpec.builder("cpu-stress-" + brokerId)
                        .disruptionType(DisruptionType.CPU_STRESS)
                        .targetBrokerId(brokerId)
                        .cpuCores(cores)
                        .chaosDurationSec(intOr(ov, "chaosDurationSec", 60))
                        .build(),
                15,
                30,
                true)));
        return plan;
    }

    private static DisruptionPlan basePlan(String name, String description) {
        DisruptionPlan plan = new DisruptionPlan();
        plan.setName(name);
        plan.setDescription(description);
        plan.setAutoRollback(true);
        plan.setBaselineDurationSec(30);
        return plan;
    }

    private static int intOr(Map<String, Object> ov, String key, int def) {
        Object v = ov.get(key);
        if (v instanceof Number n) return n.intValue();
        if (v instanceof String s) {
            try {
                return Integer.parseInt(s);
            } catch (NumberFormatException e) {
                return def;
            }
        }
        return def;
    }

    private static String stringOr(Map<String, Object> ov, String key, String def) {
        Object v = ov.get(key);
        return v instanceof String s ? s : def;
    }
}
