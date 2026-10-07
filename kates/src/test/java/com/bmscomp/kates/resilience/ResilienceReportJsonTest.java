package com.bmscomp.kates.resilience;

import static org.junit.jupiter.api.Assertions.*;

import java.time.Duration;
import java.util.List;
import jakarta.inject.Inject;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.ProbeResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.report.TestReport;

/**
 * Pins the keys and shapes the CLI reads from the report POST
 * /api/resilience writes ({@code ResilienceResult} in
 * {@code cli/client/types.go}).
 *
 * <p>kates resilience run prints the recovery time, each probe phase's
 * results and the test run's id. The CLI's struct had none of them, so
 * neither the table nor -o json showed them, and the book sent readers to
 * kates test get for a run whose id the CLI never printed.
 */
@QuarkusTest
class ResilienceReportJsonTest {

    /** The mapper ResilienceResource writes the report with. */
    @Inject
    ObjectMapper objectMapper;

    private JsonNode wire(ResilienceReport report) throws Exception {
        return objectMapper.readTree(objectMapper.writeValueAsString(report));
    }

    private static ResilienceReport completed() {
        ResilienceReport report = new ResilienceReport();
        report.setStatus("COMPLETED");
        TestReport performance = new TestReport();
        performance.setRun(new TestRun(TestType.INTEGRITY, null).withId("run-1"));
        report.setPerformanceReport(performance);
        report.setBaselineProbes(List.of(ProbeResult.pass("isr-health-check", "0", 812)));
        report.setDuringChaosProbes(List.of(
                ProbeResult.pass("isr-health-check", "12", 790), ProbeResult.fail("isr-health-check", "61", 803)));
        report.setPostRecoveryProbes(List.of(ProbeResult.fail("cluster-ready", "No Kafka CR found", 95)));
        report.setRecoveryTime(Duration.ofSeconds(12, 345_678_901));
        return report;
    }

    /**
     * A Duration is a number of seconds, its nanoseconds the fraction: the
     * CLI's BackendDuration reads that, and the default of
     * quarkus.jackson.write-durations-as-timestamps keeps it so.
     */
    @Test
    void recoveryTimeIsDecimalSeconds() throws Exception {
        JsonNode recoveryTime = wire(completed()).get("recoveryTime");

        assertTrue(recoveryTime.isNumber(), "recoveryTime is " + recoveryTime);
        assertEquals("12.345678901", recoveryTime.decimalValue().toPlainString());
    }

    /**
     * A run whose probes never all passed in one poll has no recoveryTime,
     * only unrecoveredAfter, as decimal seconds too.
     */
    @Test
    void aRunThatDidNotRecoverHasUnrecoveredAfterInstead() throws Exception {
        ResilienceReport report = completed();
        report.setRecoveryTime(null);
        report.setUnrecoveredAfter(Duration.ofSeconds(115, 250_000_000));

        JsonNode json = wire(report);

        assertFalse(json.has("recoveryTime"), "recoveryTime in " + json);
        JsonNode unrecoveredAfter = json.get("unrecoveredAfter");
        assertTrue(unrecoveredAfter != null && unrecoveredAfter.isNumber(), "unrecoveredAfter is " + unrecoveredAfter);
        assertEquals(115.25, unrecoveredAfter.asDouble(), 1e-9);
    }

    @Test
    void eachProbeResultHasTheKeysTheCliReads() throws Exception {
        JsonNode json = wire(completed());

        for (String phase : List.of("baselineProbes", "duringChaosProbes", "postRecoveryProbes")) {
            JsonNode results = json.get(phase);
            assertTrue(results != null && results.isArray() && !results.isEmpty(), phase + " is " + results);
            for (JsonNode result : results) {
                assertTrue(result.get("name").isTextual(), phase + ": " + result);
                assertTrue(result.get("passed").isBoolean(), phase + ": " + result);
                assertTrue(result.get("output").isTextual(), phase + ": " + result);
            }
        }
        assertFalse(json.at("/duringChaosProbes/1/passed").asBoolean());
        assertEquals("61", json.at("/duringChaosProbes/1/output").asText());
    }

    @Test
    void theTestRunsIdIsUnderPerformanceReportRun() throws Exception {
        assertEquals("run-1", wire(completed()).at("/performanceReport/run/id").asText());
    }

    /**
     * A report that ended ERROR after its test run started names the run at
     * the top, where the CLI reads it first: it has no performanceReport.
     */
    @Test
    void aReportNamesItsTestRunAtTheTop() throws Exception {
        ResilienceReport report = new ResilienceReport();
        report.setStatus("ERROR");
        report.setError("java.util.concurrent.TimeoutException");
        report.setTestRunId("run-1");

        JsonNode json = wire(report);

        assertTrue(json.get("testRunId").isTextual(), "testRunId is " + json.get("testRunId"));
        assertEquals("run-1", json.get("testRunId").asText());
        assertFalse(json.has("performanceReport"), "performanceReport in " + json);
    }

    /**
     * A report that ends before the recovery wait leaves those keys out, which
     * the CLI reads as nothing to print, not as a recovery time of 0.
     */
    @Test
    void aReportThatEndedEarlyLeavesTheirKeysOut() throws Exception {
        ResilienceReport report = new ResilienceReport();
        report.setStatus("ERROR");
        report.setError("The benchmark did not start: Concurrency limit reached");

        JsonNode json = wire(report);

        assertEquals("ERROR", json.get("status").asText());
        for (String key : List.of(
                "recoveryTime",
                "unrecoveredAfter",
                "baselineProbes",
                "duringChaosProbes",
                "postRecoveryProbes",
                "performanceReport")) {
            assertFalse(json.has(key), key + " in " + json);
        }
    }
}
