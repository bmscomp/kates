package com.bmscomp.kates.resilience;

import static org.junit.jupiter.api.Assertions.*;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.stream.Stream;
import jakarta.inject.Inject;
import jakarta.validation.Validator;

import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.dataformat.yaml.YAMLMapper;
import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.MethodSource;

import com.bmscomp.kates.chaos.DisruptionType;
import com.bmscomp.kates.chaos.FaultLimits;
import com.bmscomp.kates.chaos.FaultSpec;
import com.bmscomp.kates.chaos.ProbeExecutor;
import com.bmscomp.kates.chaos.ProbeSpec;
import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.ScenarioPhase;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.engine.TestOrchestrator;

/**
 * Each cli/examples/resilience-*.yaml file is a body POST /api/resilience
 * runs as the example says: kates resilience run sends the file as written.
 *
 * <p>The Kates API ignores a field it doesn't have, and a run ignores the
 * settings its test type, its fault or its probes don't read, so neither
 * fails the run: it goes ahead on other terms. The examples set numProducers
 * and numConsumers on LOAD and ENDURANCE runs, which start one producer and
 * one consumer whatever they say; they drained a node without naming it, aimed
 * a leader election at a topic, and compared probe output with ==, which the
 * probe executor read as contains. Here each of those fails its example, as
 * do a comparator or a kafkaProbe check the probe executor doesn't have, a
 * field the request classes lack, and a request the resource would refuse
 * before its stream starts.
 */
@QuarkusTest
class ResilienceExamplesTest {

    /** Relative to the kates module, where Maven runs the tests. */
    private static final Path EXAMPLES = Path.of("..", "cli", "examples");

    /** The types whose run starts a producer per numProducers (TestOrchestrator.buildTasks). */
    private static final Set<TestType> PRODUCER_PER_NUM_PRODUCERS = Set.of(TestType.STRESS, TestType.CAPACITY);

    /** ResilienceOrchestrator runs a Continuous probe during the fault too, and any other as Edge. */
    private static final List<String> MODES = List.of("Edge", "Continuous");

    @Inject
    ObjectMapper objectMapper;

    @Inject
    TestOrchestrator testOrchestrator;

    @Inject
    FaultLimits faultLimits;

    @Inject
    Validator validator;

    static Stream<String> examples() throws IOException {
        try (Stream<Path> files = Files.list(EXAMPLES)) {
            return files
                    .map(file -> file.getFileName().toString())
                    .filter(name -> name.startsWith("resilience-") && name.endsWith(".yaml"))
                    .sorted()
                    .toList()
                    .stream();
        }
    }

    /**
     * Read with FAIL_ON_UNKNOWN_PROPERTIES, the example has no field the
     * request classes lack, in its testRequest, its chaosSpec, its probes or
     * beside them.
     */
    @ParameterizedTest(name = "{0}")
    @MethodSource("examples")
    void sendsOnlyFieldsTheKatesApiReads(String file) throws IOException {
        objectMapper
                .copy()
                .enable(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES)
                .treeToValue(read(file), ResilienceTestRequest.class);
    }

    /**
     * Read as the resource reads it, the example passes the checks
     * executeResilienceTest makes before its stream starts, and the
     * constraints POST /api/tests puts on a test request.
     */
    @ParameterizedTest(name = "{0}")
    @MethodSource("examples")
    void isNotRefused(String file) throws IOException {
        ResilienceTestRequest request = objectMapper.treeToValue(read(file), ResilienceTestRequest.class);

        assertNotNull(request.getTestRequest(), "testRequest");
        assertNotNull(request.getChaosSpec(), "chaosSpec");
        assertEquals(
                Optional.empty(),
                testOrchestrator.refusal(request.getTestRequest()).map(Throwable::getMessage));
        assertEquals(Map.of(), faultLimits.violations(request.getChaosSpec()));
        assertEquals(
                List.of(),
                validator.validate(request.getTestRequest()).stream()
                        .map(v -> v.getPropertyPath() + ": " + v.getMessage())
                        .toList());
    }

    /** The example sets nothing its run reads as something else, or not at all. */
    @ParameterizedTest(name = "{0}")
    @MethodSource("examples")
    void runsAsItSays(String file) throws IOException {
        assertEquals(List.of(), misreadings(read(file)));
    }

    /**
     * The settings of an example that its run would not honour as written,
     * each with why; empty when there are none.
     */
    private List<String> misreadings(JsonNode example) throws IOException {
        ResilienceTestRequest request = objectMapper.treeToValue(example, ResilienceTestRequest.class);
        List<String> found = new ArrayList<>();
        CreateTestRequest test = request.getTestRequest();
        FaultSpec chaos = request.getChaosSpec();
        if (test == null || chaos == null) {
            found.add("testRequest and chaosSpec: a resilience run needs both");
            return found;
        }

        // A plain run starts a producer per numProducers for STRESS and
        // CAPACITY only, and reads no numConsumers (TestOrchestrator.buildTasks).
        // A scenario's phases start their own producers, and read neither
        // (buildPhaseTask).
        Map<String, TestSpec> specs = new LinkedHashMap<>();
        String producersWhy;
        if (test.isScenario()) {
            specs.put("testRequest.scenario.baseSpec", test.getScenario().getBaseSpec());
            List<ScenarioPhase> phases = test.getScenario().getPhases();
            for (int i = 0; i < phases.size(); i++) {
                specs.put(
                        "testRequest.scenario.phases[" + i + "].spec",
                        phases.get(i).getSpec());
            }
            producersWhy = "a scenario's phases don't read it; each starts its own producers";
        } else {
            specs.put("testRequest.spec", test.getSpec());
            producersWhy = PRODUCER_PER_NUM_PRODUCERS.contains(test.getType())
                    ? null
                    : test.getType() + " doesn't read it; only STRESS and CAPACITY start a producer per"
                            + " numProducers";
        }
        for (Map.Entry<String, TestSpec> entry : specs.entrySet()) {
            TestSpec spec = entry.getValue();
            if (spec != null && producersWhy != null && spec.hasNumProducers()) {
                found.add(entry.getKey() + ".numProducers: " + producersWhy);
            }
            if (spec != null && spec.hasNumConsumers()) {
                found.add(entry.getKey() + ".numConsumers: no run reads it; a LOAD or ENDURANCE run starts one"
                        + " consumer, and no other type a separate one");
            }
        }

        // Litmus's node-drain drains the node in TARGET_NODE, and Kates sets
        // none, nor gives node-drain the pods targetLabel picks
        // (LitmusChaosProvider.buildChaosEngine).
        if (chaos.disruptionType() == DisruptionType.NODE_DRAIN
                && !chaos.envOverrides().containsKey("TARGET_NODE")) {
            found.add("chaosSpec.envOverrides.TARGET_NODE: node-drain drains the node it names, and Kates sets none");
        }

        // Only a disruption plan aims a fault at a partition's leader
        // (DisruptionOrchestrator); a resilience run hands the fault to the
        // chaos provider as it is.
        for (String field : List.of("targetTopic", "targetPartition")) {
            if (example.path("chaosSpec").has(field)) {
                found.add("chaosSpec." + field + ": a resilience run aims no fault at a partition's leader;"
                        + " only a disruption plan does");
            }
        }

        List<ProbeSpec> probes = request.getProbes() != null ? request.getProbes() : List.of();
        for (int i = 0; i < probes.size(); i++) {
            ProbeSpec probe = probes.get(i);
            String at = "probes[" + i + "].";
            if (!in(MODES, probe.mode())) {
                found.add(at + "mode: " + probe.mode() + " runs as Edge; use Edge or Continuous");
            }
            if (!in(ProbeExecutor.TYPES, probe.type())) {
                found.add(at + "type: " + probe.type() + " runs as a cmdProbe; use one of " + ProbeExecutor.TYPES);
            }
            // A comparator or a kafkaProbe check the executor doesn't have
            // fails the probe before it runs.
            String probeAt = "probes[" + i + "]: ";
            ProbeExecutor.problem(probe).ifPresent(problem -> found.add(probeAt + problem));
        }
        return found;
    }

    /** Whether {@code values} holds {@code value}, which a List.of list can't be asked when it is null. */
    private static boolean in(List<String> values, String value) {
        return value != null && values.contains(value);
    }

    private static JsonNode read(String file) throws IOException {
        return new YAMLMapper().readTree(EXAMPLES.resolve(file).toFile());
    }
}
