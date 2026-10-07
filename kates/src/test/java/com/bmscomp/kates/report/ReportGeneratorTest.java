package com.bmscomp.kates.report;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.mock;

import java.util.List;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.SlaDefinition;
import com.bmscomp.kates.domain.SlaVerdict;
import com.bmscomp.kates.domain.SlaViolation;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestResult.TaskStatus;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.engine.KatesMetrics;
import com.bmscomp.kates.engine.SlaEvaluator;
import com.bmscomp.kates.export.JunitXmlExporter;
import com.bmscomp.kates.service.ClusterHealthService;

/**
 * The verdict a report gives a run, which the JUnit export, the Markdown
 * report and every client reading {@code overallSlaVerdict} pass on: a gate
 * the run could not be judged on, and a run that FAILED, must not read as met.
 */
class ReportGeneratorTest {

    private final ReportGenerator generator = new ReportGenerator();

    @BeforeEach
    void wire() {
        generator.clusterHealthService = mock(ClusterHealthService.class);
        generator.katesMetrics = mock(KatesMetrics.class);
        generator.slaEvaluator = new SlaEvaluator();
    }

    private static SlaDefinition maxP99(double ms) {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxP99LatencyMs(ms);
        return sla;
    }

    /** A row that moved records but timed none, as a Trogdor ROUND_TRIP's does. */
    private static TestResult untimed() {
        return new TestResult()
                .withTaskId("rt-1")
                .withPhaseName("round-trip")
                .withStatus(TaskStatus.DONE)
                .withRecordsSent(10_000)
                .withThroughputRecordsPerSec(1_000);
    }

    private static TestResult producer(double p99Ms) {
        return new TestResult()
                .withTaskId("produce-1")
                .withPhaseName("produce")
                .withStatus(TaskStatus.DONE)
                .withRecordsSent(10_000)
                .withThroughputRecordsPerSec(1_000)
                .withAvgLatencyMs(p99Ms / 4)
                .withP50LatencyMs(p99Ms / 5)
                .withP99LatencyMs(p99Ms)
                .withMaxLatencyMs(p99Ms * 2);
    }

    private static TestRun run(TaskStatus status, SlaDefinition sla, List<TestResult> results) {
        return new TestRun(TestType.LOAD, null).withStatus(status).withSla(sla).withResults(results);
    }

    @Test
    void latencyGateFailsAsNotMeasuredWhenNoRowMeasuredLatency() {
        TestReport report = generator.generate(run(TaskStatus.DONE, maxP99(50.0), List.of(untimed())));

        // The summary still reads 0, which is what passed the gate.
        assertEquals(0.0, report.getSummary().p99LatencyMs());
        SlaVerdict verdict = report.getOverallSlaVerdict();
        assertFalse(verdict.passed());
        assertEquals(1, verdict.violations().size());
        SlaViolation v = verdict.violations().getFirst();
        assertEquals("p99LatencyMs", v.metric());
        assertEquals("not measured", v.reason());
        assertEquals(50.0, v.threshold());

        // The phase that measured nothing is judged the same way.
        assertFalse(report.getPhases().getFirst().getSlaVerdict().passed());
    }

    @Test
    void measuredLatencyWithinItsGatePasses() {
        TestReport report = generator.generate(run(TaskStatus.DONE, maxP99(50.0), List.of(producer(40.0))));

        assertTrue(report.getOverallSlaVerdict().passed());
    }

    @Test
    void p999GateIsNeverMeasuredOnAReport() {
        // A task row keeps P50, P95, P99, max and mean, not P99.9, so the
        // summary's P99.9 is always 0: the gate cannot be judged.
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxP999LatencyMs(500.0);

        SlaVerdict verdict = generator
                .generate(run(TaskStatus.DONE, sla, List.of(producer(40.0))))
                .getOverallSlaVerdict();

        assertFalse(verdict.passed());
        assertEquals("p999LatencyMs", verdict.violations().getFirst().metric());
        assertEquals("not measured", verdict.violations().getFirst().reason());
    }

    @Test
    void failedRunFailsWithoutAnSla() {
        TestResult rejected = new TestResult()
                .withTaskId("produce-1")
                .withPhaseName("produce")
                .withStatus(TaskStatus.FAILED)
                .withError("NOT_ENOUGH_REPLICAS");

        TestReport report = generator.generate(run(TaskStatus.FAILED, null, List.of(rejected)));

        SlaVerdict verdict = report.getOverallSlaVerdict();
        assertFalse(verdict.passed());
        assertEquals(1, verdict.violations().size());
        SlaViolation status = verdict.violations().getFirst();
        assertEquals(SlaViolation.RUN_STATUS, status.metric());
        assertEquals("FAILED: NOT_ENOUGH_REPLICAS", status.reason());
        assertEquals(SlaViolation.Severity.CRITICAL, status.severity());
        // The status is the run's: a phase is judged on its SLA alone.
        assertTrue(report.getPhases().getFirst().getSlaVerdict().passed());
    }

    @Test
    void failedRunFailsALatencyOnlySlaItsRowsMeet() {
        // The latency gate itself holds, and a verdict built on the SLA alone
        // passed the run however it ended.
        TestResult measured = producer(40.0).withStatus(TaskStatus.FAILED).withError("Cancelled by user");

        SlaVerdict verdict = generator
                .generate(run(TaskStatus.FAILED, maxP99(50.0), List.of(measured)))
                .getOverallSlaVerdict();

        assertFalse(verdict.passed());
        assertEquals(1, verdict.violations().size());
        assertEquals(
                "FAILED: Cancelled by user", verdict.violations().getFirst().reason());
    }

    @Test
    void runThatFailedBeforeAnyTaskRanFailsAndNamesTheStatusFirst() {
        // A topic that could not be created: no row, no stored reason.
        SlaVerdict verdict = generator
                .generate(run(TaskStatus.FAILED, maxP99(50.0), List.of()))
                .getOverallSlaVerdict();

        assertFalse(verdict.passed());
        assertEquals(2, verdict.violations().size());
        assertEquals("FAILED before any task ran", verdict.violations().get(0).reason());
        assertEquals("not measured", verdict.violations().get(1).reason());
    }

    @Test
    void runInFlightKeepsAPassingVerdict() {
        // Nothing measured yet is not "not measured": the run is still going.
        TestReport report = generator.generate(run(TaskStatus.RUNNING, maxP99(50.0), List.of(untimed())));

        assertTrue(report.getOverallSlaVerdict().passed());
    }

    @Test
    void aRunningScenarioIsSummarisedOverTheTasksThatHaveStarted() {
        // A RAMP phase half-way through its steps, with a STEADY phase after
        // it. The next step and the STEADY phase wait for their turn, PENDING,
        // and their 0s halved the ramp's rate and more than halved the run's.
        TestResult warmup = producer(40.0).withPhaseName("warmup").withThroughputRecordsPerSec(500);
        TestResult firstStep = producer(40.0)
                .withPhaseName("ramp")
                .withStatus(TaskStatus.RUNNING)
                .withThroughputRecordsPerSec(250);
        TestResult nextStep = new TestResult().withPhaseName("ramp").withStatus(TaskStatus.PENDING);
        TestResult steady = new TestResult().withPhaseName("steady").withStatus(TaskStatus.PENDING);

        TestReport report =
                generator.generate(run(TaskStatus.RUNNING, null, List.of(warmup, firstStep, nextStep, steady)));

        assertEquals(375.0, report.getSummary().avgThroughputRecPerSec(), 0.001);
        assertEquals(
                List.of("warmup", "ramp", "steady"),
                report.getPhases().stream().map(PhaseReport::getPhaseName).toList());
        assertEquals(250.0, report.getPhases().get(1).getMetrics().avgThroughputRecPerSec(), 0.001);
        // A phase that has not started is still listed, with nothing measured.
        assertEquals(0.0, report.getPhases().get(2).getMetrics().avgThroughputRecPerSec());
    }

    @Test
    void failedRunWithNoRowsExportsOneFailingTestCase() {
        TestReport report = generator.generate(run(TaskStatus.FAILED, null, List.of()));

        String xml = new JunitXmlExporter().export(report);

        assertTrue(xml.contains("tests=\"1\" failures=\"1\""), xml);
        assertEquals(
                1, xml.lines().filter(l -> l.trim().startsWith("<testcase ")).count(), xml);
        assertTrue(xml.contains("<failure message=\"status FAILED before any task ran\""), xml);
    }

    @Test
    void violationCarriesAReasonOnlyWhenItHasOne() {
        // The CLI prints the reason in place of the -1; an ordinary violation
        // keeps the four keys it has always had.
        ObjectMapper json = new ObjectMapper();

        JsonNode notMeasured =
                json.valueToTree(SlaViolation.notMeasured("p99LatencyMs", 50.0, SlaViolation.Severity.CRITICAL));
        assertEquals("not measured", notMeasured.get("reason").asText());
        assertEquals(-1.0, notMeasured.get("actual").asDouble());

        JsonNode breach = json.valueToTree(SlaViolation.critical("p99LatencyMs", 50.0, 75.0));
        assertFalse(breach.has("reason"), breach.toString());
        assertEquals(4, breach.size(), breach.toString());
    }

    @Test
    void markdownShowsWhyAViolationHasNoNumber() {
        TestReport report = generator.generate(run(TaskStatus.FAILED, maxP99(50.0), List.of()));

        String markdown = generator.toMarkdown(report);

        assertTrue(markdown.contains("| status | — | FAILED before any task ran | CRITICAL |"), markdown);
        assertTrue(markdown.contains("| p99LatencyMs | 50.00 | not measured | CRITICAL |"), markdown);
    }
}
