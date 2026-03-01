package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import java.util.Map;

import org.junit.jupiter.api.Test;

class FaultSpecTest {

    @Test
    void builderDefaults() {
        FaultSpec spec = FaultSpec.builder("test-exp").build();

        assertEquals("test-exp", spec.experimentName());
        assertEquals("kafka", spec.targetNamespace());
        assertEquals("strimzi.io/component-type=kafka", spec.targetLabel());
        assertEquals("", spec.targetPod());
        assertEquals(30, spec.chaosDurationSec());
        assertEquals(0, spec.delayBeforeSec());
        assertTrue(spec.envOverrides().isEmpty());
        assertNull(spec.disruptionType());
        assertEquals(-1, spec.targetBrokerId());
        assertEquals(100, spec.networkLatencyMs());
        assertEquals(80, spec.fillPercentage());
        assertEquals(1, spec.cpuCores());
        assertEquals(500, spec.memoryMb());
        assertEquals(2, spec.ioWorkers());
        assertEquals(30, spec.gracePeriodSec());
        assertEquals("", spec.targetTopic());
        assertEquals(0, spec.targetPartition());
        assertTrue(spec.probes().isEmpty());
    }

    @Test
    void builderOverridesAllFields() {
        ProbeSpec probe = ProbeSpec.builder("p1").build();

        FaultSpec spec = FaultSpec.builder("custom")
                .targetNamespace("test-ns")
                .targetLabel("app=kafka")
                .targetPod("broker-0")
                .chaosDurationSec(120)
                .delayBeforeSec(10)
                .envOverrides(Map.of("KEY", "VAL"))
                .disruptionType(DisruptionType.MEMORY_STRESS)
                .targetBrokerId(2)
                .networkLatencyMs(200)
                .fillPercentage(90)
                .cpuCores(4)
                .memoryMb(1024)
                .ioWorkers(8)
                .gracePeriodSec(60)
                .targetTopic("my-topic")
                .targetPartition(3)
                .probes(List.of(probe))
                .build();

        assertEquals("custom", spec.experimentName());
        assertEquals("test-ns", spec.targetNamespace());
        assertEquals("broker-0", spec.targetPod());
        assertEquals(120, spec.chaosDurationSec());
        assertEquals(DisruptionType.MEMORY_STRESS, spec.disruptionType());
        assertEquals(1024, spec.memoryMb());
        assertEquals(8, spec.ioWorkers());
        assertEquals("my-topic", spec.targetTopic());
        assertEquals(1, spec.probes().size());
    }

    @Test
    void envOverridesAreImmutableCopies() {
        var mutable = new java.util.HashMap<String, String>();
        mutable.put("A", "1");

        FaultSpec spec = FaultSpec.builder("test")
                .envOverrides(mutable)
                .build();

        mutable.put("B", "2");
        assertEquals(1, spec.envOverrides().size());
        assertThrows(UnsupportedOperationException.class, () -> spec.envOverrides().put("C", "3"));
    }

    @Test
    void probesAreImmutableCopies() {
        var mutable = new java.util.ArrayList<ProbeSpec>();
        mutable.add(ProbeSpec.builder("p1").build());

        FaultSpec spec = FaultSpec.builder("test")
                .probes(mutable)
                .build();

        mutable.add(ProbeSpec.builder("p2").build());
        assertEquals(1, spec.probes().size());
        assertThrows(UnsupportedOperationException.class, () -> spec.probes().add(ProbeSpec.builder("p3").build()));
    }
}
