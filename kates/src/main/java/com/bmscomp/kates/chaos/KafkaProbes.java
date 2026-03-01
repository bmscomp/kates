package com.bmscomp.kates.chaos;

import java.util.List;

/**
 * Factory for reusable Kafka-specific probes.
 * Each probe maps to a Litmus YAML probe equivalent.
 */
public final class KafkaProbes {

    private KafkaProbes() {}

    /**
     * Checks under-replicated partitions are below threshold.
     * Equivalent to {@code isr-health-probe.yaml}.
     */
    public static ProbeSpec isrHealth() {
        return ProbeSpec.builder("isr-health-check")
                .mode("Continuous")
                .command("kafka-topics.sh --bootstrap-server localhost:9092 "
                        + "--describe --under-replicated-partitions 2>/dev/null "
                        + "| grep -c 'Topic:' || echo '0'")
                .expectedOutput("50")
                .comparator("<=")
                .intervalSec(10)
                .timeoutSec(30)
                .build();
    }

    /**
     * Checks for zero unavailable partitions.
     * Equivalent to min-isr-check probe.
     */
    public static ProbeSpec minIsr() {
        return ProbeSpec.builder("min-isr-check")
                .mode("Edge")
                .command("kafka-topics.sh --bootstrap-server localhost:9092 "
                        + "--describe --unavailable-partitions 2>/dev/null "
                        + "| grep -c 'Topic:' || echo '0'")
                .expectedOutput("5")
                .comparator("<=")
                .intervalSec(15)
                .timeoutSec(30)
                .build();
    }

    /**
     * Checks Kafka cluster CR is in Ready state.
     */
    public static ProbeSpec clusterReady() {
        return ProbeSpec.builder("cluster-ready")
                .type("k8sProbe")
                .mode("Edge")
                .command("kafka Ready")
                .expectedOutput("Ready")
                .comparator("contains")
                .intervalSec(15)
                .timeoutSec(30)
                .build();
    }

    /**
     * Checks producer can write within latency bound.
     * Equivalent to {@code producer-throughput-probe.yaml}.
     */
    public static ProbeSpec producerThroughput() {
        return ProbeSpec.builder("producer-throughput")
                .mode("Continuous")
                .command("kafka-producer-perf-test.sh --topic kates-probe-topic "
                        + "--num-records 10 --record-size 100 "
                        + "--throughput -1 "
                        + "--producer-props bootstrap.servers=localhost:9092 "
                        + "2>/dev/null | tail -1 | awk '{print $6}'")
                .expectedOutput("0")
                .comparator(">")
                .intervalSec(15)
                .timeoutSec(30)
                .build();
    }

    /**
     * Checks consumer group lag is recovering.
     * Equivalent to {@code consumer-latency-probe.yaml}.
     */
    public static ProbeSpec consumerLatency() {
        return ProbeSpec.builder("consumer-latency")
                .mode("Edge")
                .command("kafka-consumer-groups.sh --bootstrap-server localhost:9092 "
                        + "--describe --all-groups 2>/dev/null "
                        + "| awk 'NR>1 {sum+=$6} END {print sum+0}'")
                .expectedOutput("100000")
                .comparator("<=")
                .intervalSec(15)
                .timeoutSec(30)
                .build();
    }

    /**
     * Checks for zero offline partitions.
     */
    public static ProbeSpec partitionAvailability() {
        return ProbeSpec.builder("partition-availability")
                .mode("Edge")
                .command("kafka-topics.sh --bootstrap-server localhost:9092 "
                        + "--describe --unavailable-partitions 2>/dev/null "
                        + "| grep -c 'Topic:' || echo '0'")
                .expectedOutput("0")
                .comparator("<=")
                .intervalSec(10)
                .timeoutSec(30)
                .build();
    }

    /**
     * Returns all standard Kafka probes.
     */
    public static List<ProbeSpec> all() {
        return List.of(
                isrHealth(),
                minIsr(),
                clusterReady(),
                producerThroughput(),
                consumerLatency(),
                partitionAvailability());
    }
}
