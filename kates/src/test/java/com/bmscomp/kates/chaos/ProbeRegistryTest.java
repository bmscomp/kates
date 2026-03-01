package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.EnumSource;

class ProbeRegistryTest {

    @ParameterizedTest
    @EnumSource(DisruptionType.class)
    void everyDisruptionTypeMapsToNonEmptyProbes(DisruptionType type) {
        List<ProbeSpec> probes = ProbeRegistry.defaultProbesFor(type);
        assertNotNull(probes);
        assertFalse(probes.isEmpty(), "No default probes for " + type);
    }

    @Test
    void podDeleteMapsToIsrHealthAndClusterReady() {
        List<ProbeSpec> probes = ProbeRegistry.defaultProbesFor(DisruptionType.POD_DELETE);
        assertEquals(2, probes.size());
        assertEquals("isr-health-check", probes.get(0).name());
        assertEquals("cluster-ready", probes.get(1).name());
    }

    @Test
    void memoryStressMapsToThroughputAndClusterReady() {
        List<ProbeSpec> probes = ProbeRegistry.defaultProbesFor(DisruptionType.MEMORY_STRESS);
        assertEquals(2, probes.size());
        assertEquals("producer-throughput", probes.get(0).name());
        assertEquals("cluster-ready", probes.get(1).name());
    }

    @Test
    void resolveReturnsExplicitProbesWhenSet() {
        ProbeSpec custom = ProbeSpec.builder("custom-probe").build();
        FaultSpec spec = FaultSpec.builder("test")
                .disruptionType(DisruptionType.POD_DELETE)
                .probes(List.of(custom))
                .build();

        List<ProbeSpec> resolved = ProbeRegistry.resolve(spec);
        assertEquals(1, resolved.size());
        assertEquals("custom-probe", resolved.get(0).name());
    }

    @Test
    void resolveAutoAttachesWhenProbesEmpty() {
        FaultSpec spec = FaultSpec.builder("test")
                .disruptionType(DisruptionType.DNS_ERROR)
                .build();

        List<ProbeSpec> resolved = ProbeRegistry.resolve(spec);
        assertEquals(2, resolved.size());
        assertEquals("cluster-ready", resolved.get(0).name());
        assertEquals("consumer-latency", resolved.get(1).name());
    }

    @Test
    void resolveWithNoDisruptionTypeFallsBackToDefaults() {
        FaultSpec spec = FaultSpec.builder("test").build();

        List<ProbeSpec> resolved = ProbeRegistry.resolve(spec);
        assertFalse(resolved.isEmpty());
        assertEquals("isr-health-check", resolved.get(0).name());
    }
}
