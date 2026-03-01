package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import org.junit.jupiter.api.Test;

class ProbeSpecTest {

    @Test
    void builderDefaultValues() {
        ProbeSpec spec = ProbeSpec.builder("test-probe").build();

        assertEquals("test-probe", spec.name());
        assertEquals("cmdProbe", spec.type());
        assertEquals("Edge", spec.mode());
        assertEquals("", spec.command());
        assertEquals("", spec.expectedOutput());
        assertEquals("contains", spec.comparator());
        assertEquals(10, spec.intervalSec());
        assertEquals(30, spec.timeoutSec());
    }

    @Test
    void builderOverridesAllFields() {
        ProbeSpec spec = ProbeSpec.builder("custom")
                .type("k8sProbe")
                .mode("Continuous")
                .command("kubectl get pods")
                .expectedOutput("Running")
                .comparator("equal")
                .intervalSec(5)
                .timeoutSec(60)
                .build();

        assertEquals("custom", spec.name());
        assertEquals("k8sProbe", spec.type());
        assertEquals("Continuous", spec.mode());
        assertEquals("kubectl get pods", spec.command());
        assertEquals("Running", spec.expectedOutput());
        assertEquals("equal", spec.comparator());
        assertEquals(5, spec.intervalSec());
        assertEquals(60, spec.timeoutSec());
    }

    @Test
    void recordEquality() {
        ProbeSpec a = ProbeSpec.builder("probe").command("echo ok").build();
        ProbeSpec b = ProbeSpec.builder("probe").command("echo ok").build();
        assertEquals(a, b);
        assertEquals(a.hashCode(), b.hashCode());
    }

    @Test
    void recordInequality() {
        ProbeSpec a = ProbeSpec.builder("probe-a").build();
        ProbeSpec b = ProbeSpec.builder("probe-b").build();
        assertNotEquals(a, b);
    }
}
