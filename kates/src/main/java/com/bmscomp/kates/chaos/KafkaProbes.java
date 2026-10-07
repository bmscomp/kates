package com.bmscomp.kates.chaos;

import java.util.List;

/**
 * The built-in probes: what {@link ProbeRegistry} gives a fault that declares
 * none, and what the resilience scenarios run. The Kafka ones are
 * {@code kafkaProbe}s, which Kates evaluates over its own authenticated
 * connection ({@link KafkaProbeChecks}); each is named after the Litmus YAML
 * probe it stands for.
 *
 * <p>They used to run the Kafka CLI in a broker pod, without the SCRAM
 * credentials the brokers' 9092 listener asks for, and printed 0 when the CLI
 * failed. The ISR and partition probes passed whenever they could not ask,
 * and the producer probe wrote to a topic nothing created.
 */
public final class KafkaProbes {

    /**
     * The topic {@link #producerThroughput()} writes to. The brokers create no
     * topic on first use, so the kafka-cluster chart's platform profile
     * creates this one.
     */
    public static final String PROBE_TOPIC = "kates-probe-topic";

    private KafkaProbes() {}

    /**
     * At most 50 under-replicated partitions.
     * Equivalent to {@code isr-health-probe.yaml}.
     */
    public static ProbeSpec isrHealth() {
        return ProbeSpec.builder("isr-health-check")
                .type("kafkaProbe")
                .mode("Continuous")
                .command(KafkaProbeChecks.UNDER_REPLICATED_PARTITIONS)
                .expectedOutput("50")
                .comparator("<=")
                .intervalSec(10)
                .timeoutSec(30)
                .build();
    }

    /**
     * At most 5 unavailable partitions, as many as the Litmus min-isr-check
     * probe in {@code isr-health-probe.yaml} allows during chaos.
     */
    public static ProbeSpec minIsr() {
        return ProbeSpec.builder("min-isr-check")
                .type("kafkaProbe")
                .mode("Edge")
                .command(KafkaProbeChecks.UNAVAILABLE_PARTITIONS)
                .expectedOutput("5")
                .comparator("<=")
                .intervalSec(15)
                .timeoutSec(30)
                .build();
    }

    /**
     * The Kafka resource's Ready condition is True.
     */
    public static ProbeSpec clusterReady() {
        return ProbeSpec.builder("cluster-ready")
                .type("k8sProbe")
                .mode("Edge")
                .command("kafka Ready")
                .expectedOutput("Ready=True")
                .comparator("equal")
                .intervalSec(15)
                .timeoutSec(30)
                .build();
    }

    /**
     * Records sent to {@link #PROBE_TOPIC} with {@code acks=all} are
     * acknowledged: the probe prints the acknowledged records per second, and
     * fails when none is.
     * Equivalent to {@code producer-throughput-probe.yaml}.
     */
    public static ProbeSpec producerThroughput() {
        return ProbeSpec.builder("producer-throughput")
                .type("kafkaProbe")
                .mode("Continuous")
                .command(KafkaProbeChecks.PRODUCE + " " + PROBE_TOPIC)
                .expectedOutput("0")
                .comparator(">")
                .intervalSec(15)
                .timeoutSec(30)
                .build();
    }

    /**
     * The lag of every consumer group adds up to at most 100,000 records.
     * Equivalent to {@code consumer-latency-probe.yaml}.
     */
    public static ProbeSpec consumerLatency() {
        return ProbeSpec.builder("consumer-latency")
                .type("kafkaProbe")
                .mode("Edge")
                .command(KafkaProbeChecks.CONSUMER_LAG)
                .expectedOutput("100000")
                .comparator("<=")
                .intervalSec(15)
                .timeoutSec(30)
                .build();
    }

    /**
     * No unavailable partitions.
     */
    public static ProbeSpec partitionAvailability() {
        return ProbeSpec.builder("partition-availability")
                .type("kafkaProbe")
                .mode("Edge")
                .command(KafkaProbeChecks.UNAVAILABLE_PARTITIONS)
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
