package com.bmscomp.kates.domain;

import static org.junit.jupiter.api.Assertions.assertEquals;

import java.io.IOException;
import java.nio.file.Path;
import java.util.Set;
import java.util.TreeSet;
import java.util.stream.Collectors;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.introspect.BeanPropertyDefinition;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;

/**
 * The API's request classes against the schema the operator's CRD is generated
 * from: a field added to one of them fails here until the schema has it, since
 * the apiserver prunes a key the CRD does not know.
 */
class WireSchemaTest {

    private static final Path SCHEMA = Path.of("..", "cli", "schemas", "test-request.v1.schema.json");

    /** Keys kates lint and the LSP accept and the API does not bind; crdgen drops the same list. */
    private static final Set<String> FILE_ONLY = Set.of(
            "slaDefinition.p95LatencyMs",
            "slaDefinition.p99LatencyMs",
            "slaDefinition.avgLatencyMs",
            "slaDefinition.maxErrorRatePercent",
            "slaDefinition.maxRecoveryTimeMs",
            "slaDefinition.maxThroughputDegradationPercent",
            "slaDefinition.maxIsrRecoveryTimeMs",
            "slaDefinition.maxConsumerLag",
            "scenarioPhase.type",
            "scenarioPhase.specOverrides");

    @ParameterizedTest
    @CsvSource({
        "com.bmscomp.kates.domain.TestSpec, wireTestSpec",
        "com.bmscomp.kates.domain.SlaDefinition, slaDefinition",
        "com.bmscomp.kates.domain.TestScenario, multiPhaseScenario",
        "com.bmscomp.kates.domain.ScenarioPhase, scenarioPhase"
    })
    void jacksonBindsExactlyWhatTheSchemaDefines(Class<?> type, String def) throws IOException {
        ObjectMapper mapper = new ObjectMapper();
        Set<String> bound =
                mapper.getDeserializationConfig().introspect(mapper.constructType(type)).findProperties().stream()
                        .filter(BeanPropertyDefinition::couldDeserialize)
                        .map(BeanPropertyDefinition::getName)
                        .collect(Collectors.toCollection(TreeSet::new));

        Set<String> schema = new TreeSet<>();
        JsonNode properties = mapper.readTree(SCHEMA.toFile()).at("/$defs/" + def + "/properties");
        properties.fieldNames().forEachRemaining(name -> {
            if (!FILE_ONLY.contains(def + "." + name)) {
                schema.add(name);
            }
        });

        assertEquals(schema, bound);
    }
}
