package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;

import java.util.Arrays;
import java.util.List;
import java.util.Map;
import jakarta.validation.Validation;

import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.ScenarioPhase;
import com.bmscomp.kates.domain.TestScenario;
import com.bmscomp.kates.domain.TestSpec;

class SpecLimitsTest {

    private final SpecLimits limits =
            new SpecLimits(Validation.buildDefaultValidatorFactory().getValidator());

    @Test
    void aSpecsValuesAreKeyedByNameInTheSameOrderEachTime() {
        TestSpec spec = new TestSpec();
        spec.setTopic("not a topic!");
        spec.setNumRecords(0);
        spec.setAcks("2");

        Map<String, String> found = limits.violations(spec);

        assertEquals(List.of("acks", "numRecords", "topic"), List.copyOf(found.keySet()));
        assertEquals("acks must be one of: all, -1, 0, 1", found.get("acks"));
        assertEquals("topic must be a legal Kafka topic name", found.get("topic"));
        assertEquals(Map.of(), limits.violations(new TestSpec()));
        assertEquals(Map.of(), limits.violations((TestSpec) null));
    }

    @Test
    void aScenariosValuesAreKeyedByTheirPathInTheScenario() {
        TestSpec base = new TestSpec();
        base.setCompressionType("brotli");
        TestSpec own = new TestSpec();
        own.setThroughput(-5);
        ScenarioPhase steady = new ScenarioPhase("steady", ScenarioPhase.PhaseType.STEADY, 0, -1);
        steady.setSpec(own);
        // A null phase, and a phase without a spec, have nothing to check.
        TestScenario scenario = new TestScenario();
        scenario.setBaseSpec(base);
        scenario.setPhases(
                Arrays.asList(null, new ScenarioPhase("warmup", ScenarioPhase.PhaseType.WARMUP, 0, -1), steady));

        assertEquals(
                Map.of(
                        "baseSpec.compressionType", "compressionType must be one of: none, gzip, snappy, lz4, zstd",
                        "phases[2].spec.throughput", "throughput must be -1 (unlimited) or positive"),
                limits.violations(scenario));
    }

    @Test
    void aScenarioWithoutPhasesHasItsBaseSpecChecked() {
        TestSpec base = new TestSpec();
        base.setPartitions(0);
        TestScenario scenario = new TestScenario();
        scenario.setBaseSpec(base);
        scenario.setPhases(null);

        assertEquals(
                List.of("baseSpec.partitions"),
                List.copyOf(limits.violations(scenario).keySet()));
        assertEquals(Map.of(), limits.violations((TestScenario) null));
    }
}
