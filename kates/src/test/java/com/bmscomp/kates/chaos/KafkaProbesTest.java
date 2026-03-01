package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;

import org.junit.jupiter.api.Test;

class KafkaProbesTest {

    @Test
    void isrHealthProbeHasCorrectProperties() {
        ProbeSpec p = KafkaProbes.isrHealth();
        assertEquals("isr-health-check", p.name());
        assertEquals("cmdProbe", p.type());
        assertEquals("Continuous", p.mode());
        assertEquals("<=", p.comparator());
        assertFalse(p.command().isEmpty());
    }

    @Test
    void minIsrProbeHasCorrectProperties() {
        ProbeSpec p = KafkaProbes.minIsr();
        assertEquals("min-isr-check", p.name());
        assertEquals("Edge", p.mode());
        assertEquals("<=", p.comparator());
    }

    @Test
    void clusterReadyProbeIsK8sType() {
        ProbeSpec p = KafkaProbes.clusterReady();
        assertEquals("cluster-ready", p.name());
        assertEquals("k8sProbe", p.type());
        assertEquals("contains", p.comparator());
    }

    @Test
    void producerThroughputHasContinuousMode() {
        ProbeSpec p = KafkaProbes.producerThroughput();
        assertEquals("producer-throughput", p.name());
        assertEquals("Continuous", p.mode());
        assertEquals(">", p.comparator());
    }

    @Test
    void consumerLatencyHasEdgeMode() {
        ProbeSpec p = KafkaProbes.consumerLatency();
        assertEquals("consumer-latency", p.name());
        assertEquals("Edge", p.mode());
        assertEquals("<=", p.comparator());
    }

    @Test
    void partitionAvailabilityChecksZero() {
        ProbeSpec p = KafkaProbes.partitionAvailability();
        assertEquals("partition-availability", p.name());
        assertEquals("0", p.expectedOutput());
        assertEquals("<=", p.comparator());
    }

    @Test
    void allReturnsSixProbes() {
        List<ProbeSpec> all = KafkaProbes.all();
        assertEquals(6, all.size());
    }

    @Test
    void allProbesHaveUniqueNames() {
        List<ProbeSpec> all = KafkaProbes.all();
        long uniqueNames = all.stream().map(ProbeSpec::name).distinct().count();
        assertEquals(all.size(), uniqueNames);
    }
}
