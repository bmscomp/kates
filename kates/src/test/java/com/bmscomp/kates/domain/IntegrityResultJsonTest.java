package com.bmscomp.kates.domain;

import static org.junit.jupiter.api.Assertions.*;

import java.time.Duration;
import java.util.List;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;

/**
 * Pins the JSON keys the CLI reads from an integrity result
 * ({@code IntegrityResult} in {@code cli/client/types.go}).
 *
 * <p>The record's components are {@code Duration}s, which Jackson writes as
 * decimal seconds under {@code producerRto}, {@code maxRto} and {@code rpo}.
 * The CLI reads {@code producerRtoMs}, {@code maxRtoMs}, {@code rpoMs} and
 * {@code verdict} — derived accessors Jackson skipped until they were
 * annotated, which left the CLI's RTO/RPO gates comparing against nothing.
 */
class IntegrityResultJsonTest {

    // The Quarkus-managed mapper registers every module on the classpath,
    // JavaTimeModule included, and has no customizer in this project.
    private final ObjectMapper mapper = new ObjectMapper().findAndRegisterModules();

    /** Every key the CLI's IntegrityResult struct declares. */
    private static final List<String> CLI_KEYS = List.of(
            "totalSent",
            "totalAcked",
            "totalConsumed",
            "lostRecords",
            "duplicateRecords",
            "dataLossPercent",
            "lostRanges",
            "producerRtoMs",
            "consumerRtoMs",
            "maxRtoMs",
            "rpoMs",
            "outOfOrderCount",
            "crcFailures",
            "orderingVerified",
            "crcVerified",
            "idempotenceEnabled",
            "transactionsEnabled",
            "verdict",
            "timeline");

    private static IntegrityResult result(long lost, Duration rpo) {
        return new IntegrityResult(
                1000,
                1000,
                1000 - lost,
                lost,
                0,
                lost / 10.0,
                List.of(),
                Duration.ofMillis(1500),
                Duration.ofMillis(800),
                Duration.ofMillis(1500),
                rpo,
                List.of(),
                0,
                0,
                true,
                true,
                false,
                false,
                List.of());
    }

    private JsonNode wire(IntegrityResult result) throws Exception {
        return mapper.readTree(mapper.writeValueAsString(result));
    }

    @Test
    void serializesEveryKeyTheCliReads() throws Exception {
        JsonNode json = wire(result(0, Duration.ofMillis(250)));

        for (String key : CLI_KEYS) {
            assertTrue(json.has(key), "missing key the CLI reads: " + key + " in " + json);
        }
    }

    @Test
    void millisecondKeysCarryMillisecondsAndVerdictIsPresent() throws Exception {
        JsonNode json = wire(result(0, Duration.ofMillis(250)));

        assertEquals(1500.0, json.get("producerRtoMs").asDouble(), 1e-9);
        assertEquals(800.0, json.get("consumerRtoMs").asDouble(), 1e-9);
        assertEquals(1500.0, json.get("maxRtoMs").asDouble(), 1e-9);
        assertEquals(250.0, json.get("rpoMs").asDouble(), 1e-9);
        assertEquals("PASS", json.get("verdict").asText());
    }

    @Test
    void unmeasuredRpoSerializesAsNegativeNotZero() throws Exception {
        JsonNode json = wire(result(5, null));

        // -1 is "not measured": the CLI must not pass a maxRpoMs gate on it.
        assertEquals(-1.0, json.get("rpoMs").asDouble(), 1e-9);
        assertTrue(json.get("rpo").isNull());
        assertEquals("DATA_LOSS", json.get("verdict").asText());
    }

    @Test
    void measuredZeroRpoStaysZero() throws Exception {
        JsonNode json = wire(result(0, Duration.ZERO));

        assertEquals(0.0, json.get("rpoMs").asDouble(), 1e-9);
    }
}
