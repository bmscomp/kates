package com.bmscomp.kates.resilience;

import static org.junit.jupiter.api.Assertions.*;

import java.io.IOException;
import java.nio.file.Path;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.stream.Stream;
import java.util.stream.StreamSupport;
import jakarta.inject.Inject;
import jakarta.validation.Validator;

import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.MethodSource;

import com.bmscomp.kates.chaos.FaultLimits;
import com.bmscomp.kates.engine.TestOrchestrator;

/**
 * Each example in docs/tutorials/resilience.json is a body POST
 * /api/resilience runs as written.
 *
 * <p>The Kates API ignores a field it doesn't have, so a misnamed one is
 * dropped without a word. The examples sent producers, linger, durationSec and
 * other fields TestSpec doesn't have, and ran on the defaults instead: the
 * ENDURANCE example was set to last an hour, not the five minutes it asked for.
 * Here such a field fails its example, and so does a request the resource
 * would refuse before its stream starts.
 */
@QuarkusTest
class ResilienceTutorialTest {

    /** Relative to the kates module, where Maven runs the tests. */
    private static final Path TUTORIAL = Path.of("docs", "tutorials", "resilience.json");

    @Inject
    ObjectMapper objectMapper;

    @Inject
    TestOrchestrator testOrchestrator;

    @Inject
    FaultLimits faultLimits;

    @Inject
    Validator validator;

    static Stream<String> examples() throws IOException {
        return StreamSupport.stream(read().spliterator(), false)
                .map(example -> example.get("_name").asText());
    }

    /**
     * Read with FAIL_ON_UNKNOWN_PROPERTIES, the example has no field the
     * request classes lack, in its testRequest, its chaosSpec, its probes or
     * beside them. A key that starts with an underscore is a note for the
     * reader, which the API ignores like any field it doesn't have, so the
     * example's own notes are left out first.
     */
    @ParameterizedTest(name = "{0}")
    @MethodSource("examples")
    void sendsOnlyFieldsTheKatesApiReads(String name) throws IOException {
        ObjectNode request = example(name).deepCopy();
        request.remove(request.properties().stream()
                .map(Map.Entry::getKey)
                .filter(key -> key.startsWith("_"))
                .toList());

        objectMapper
                .copy()
                .enable(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES)
                .treeToValue(request, ResilienceTestRequest.class);
    }

    /**
     * Read as the resource reads it, the example passes the checks
     * executeResilienceTest makes before its stream starts, and the
     * constraints POST /api/tests puts on a test request, which
     * POST /api/resilience doesn't check.
     */
    @ParameterizedTest(name = "{0}")
    @MethodSource("examples")
    void isNotRefused(String name) throws IOException {
        ResilienceTestRequest request = objectMapper.treeToValue(example(name), ResilienceTestRequest.class);

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

    private static JsonNode read() throws IOException {
        return new ObjectMapper().readTree(TUTORIAL.toFile()).get("examples");
    }

    private static JsonNode example(String name) throws IOException {
        for (JsonNode example : read()) {
            if (name.equals(example.get("_name").asText())) {
                return example;
            }
        }
        throw new IllegalArgumentException("No example named " + name + " in " + TUTORIAL);
    }
}
