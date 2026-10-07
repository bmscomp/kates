package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import java.util.Optional;

import org.junit.jupiter.api.Test;

/**
 * The built-in probes ask the cluster with Kates' own Kafka connection. They
 * used to run the Kafka CLI in a broker without the credentials its listener
 * asks for, print 0 when it failed, and write to a topic nothing created.
 */
class KafkaProbesTest {

    @Test
    void isrHealthAllowsFiftyUnderReplicatedPartitions() {
        ProbeSpec p = KafkaProbes.isrHealth();
        assertEquals("isr-health-check", p.name());
        assertEquals("kafkaProbe", p.type());
        assertEquals("Continuous", p.mode());
        assertEquals("under-replicated-partitions", p.command());
        assertEquals("<=", p.comparator());
        assertEquals("50", p.expectedOutput());
    }

    @Test
    void minIsrAllowsFiveUnavailablePartitions() {
        ProbeSpec p = KafkaProbes.minIsr();
        assertEquals("min-isr-check", p.name());
        assertEquals("kafkaProbe", p.type());
        assertEquals("Edge", p.mode());
        assertEquals("unavailable-partitions", p.command());
        assertEquals("<=", p.comparator());
        assertEquals("5", p.expectedOutput());
    }

    /** contains "Ready" passed a NotReady condition too. */
    @Test
    void clusterReadyWantsTheReadyConditionTrue() {
        ProbeSpec p = KafkaProbes.clusterReady();
        assertEquals("cluster-ready", p.name());
        assertEquals("k8sProbe", p.type());
        assertEquals("kafka Ready", p.command());
        assertEquals("equal", p.comparator());
        assertEquals("Ready=True", p.expectedOutput());
    }

    @Test
    void producerThroughputWritesToTheProbeTopic() {
        ProbeSpec p = KafkaProbes.producerThroughput();
        assertEquals("producer-throughput", p.name());
        assertEquals("kafkaProbe", p.type());
        assertEquals("Continuous", p.mode());
        assertEquals("produce kates-probe-topic", p.command());
        assertEquals(">", p.comparator());
        assertEquals("0", p.expectedOutput());
    }

    @Test
    void consumerLatencyAddsUpEveryGroupsLag() {
        ProbeSpec p = KafkaProbes.consumerLatency();
        assertEquals("consumer-latency", p.name());
        assertEquals("kafkaProbe", p.type());
        assertEquals("Edge", p.mode());
        assertEquals("consumer-lag", p.command());
        assertEquals("<=", p.comparator());
        assertEquals("100000", p.expectedOutput());
    }

    @Test
    void partitionAvailabilityAllowsNoUnavailablePartition() {
        ProbeSpec p = KafkaProbes.partitionAvailability();
        assertEquals("partition-availability", p.name());
        assertEquals("kafkaProbe", p.type());
        assertEquals("unavailable-partitions", p.command());
        assertEquals("<=", p.comparator());
        assertEquals("0", p.expectedOutput());
    }

    @Test
    void allReturnsSixProbesWithUniqueNames() {
        List<ProbeSpec> all = KafkaProbes.all();
        assertEquals(6, all.size());
        assertEquals(all.size(), all.stream().map(ProbeSpec::name).distinct().count());
    }

    /**
     * Each one has a type the executor runs as such, a comparator it has with
     * something to compare, and, for a kafkaProbe, a check with its arguments.
     */
    @Test
    void everyProbeIsOneTheExecutorRunsAsWritten() {
        for (ProbeSpec p : KafkaProbes.all()) {
            assertTrue(ProbeExecutor.TYPES.contains(p.type()), p.name());
            assertEquals(Optional.empty(), ProbeExecutor.problem(p), p.name());
        }
    }
}
