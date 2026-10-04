package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.*;

import java.time.Duration;
import java.util.List;
import java.util.Map;

import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.DisruptionType;
import com.bmscomp.kates.domain.SlaDefinition;
import com.bmscomp.kates.report.ReportSummary;

/**
 * A disruption plan's SLA is graded from Prometheus broker metrics and pod
 * recovery times. Constraints with no such measurement used to be skipped (data
 * loss, RPO, record count) or compared against the capture's hard-coded zeros
 * (p99.9, error rate), and an SLA made only of them graded A.
 */
class SlaGraderTest {

    private final SlaGrader grader = new SlaGrader();

    /** Shaped like {@code PrometheusMetricsCapture.toReportSummary}: no p99.9, no error rate. */
    private static ReportSummary prometheusSummary(double p99LatencyMs) {
        return new ReportSummary(0L, 900, 900, 0.9, 20, 0, 0, p99LatencyMs, 0, 0, 0L, 0, 60_000);
    }

    private static DisruptionReport.StepReport step(
            String name,
            ReportSummary post,
            Duration timeToAllReady,
            List<String> unmeasuredMetrics,
            Duration unrecoveredAfter) {
        return new DisruptionReport.StepReport(
                name,
                DisruptionType.POD_KILL,
                null,
                List.of(),
                null,
                timeToAllReady,
                null,
                null,
                post,
                Map.of("throughputRecPerSec", -12.5, "p99LatencyMs", 180.0),
                null,
                null,
                null,
                false,
                null,
                unmeasuredMetrics,
                unrecoveredAfter);
    }

    private static DisruptionReport report(DisruptionReport.StepReport... steps) {
        DisruptionReport report = new DisruptionReport();
        report.setStepReports(List.of(steps));
        return report;
    }

    private static DisruptionReport report(ReportSummary post, Duration timeToAllReady) {
        return report(step("kill-leader", post, timeToAllReady, List.of(), null));
    }

    private static List<String> fields(SlaGrader.SlaVerdict verdict) {
        return verdict.unevaluated().stream()
                .map(u -> u.substring(0, u.indexOf(':')))
                .toList();
    }

    @Test
    void dataLossAndRpoAreReportedAsNotEvaluatedRatherThanPassed() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxDataLossPercent(0.0);
        sla.setMaxRpoMs(0L);
        sla.setMinRecordsProcessed(1_000L);

        SlaGrader.SlaVerdict verdict = grader.grade(report(prometheusSummary(40), Duration.ofSeconds(20)), sla);

        assertEquals(SlaGrader.NOT_GRADED, verdict.grade(), "nothing was checked, so it is not an A");
        assertFalse(verdict.violated());
        assertEquals(0, verdict.totalChecks());
        assertEquals(List.of("maxDataLossPercent", "maxRpoMs", "minRecordsProcessed"), fields(verdict));
    }

    @Test
    void metricsThePrometheusCaptureNeverFillsAreNotComparedAgainstItsZeros() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxP999LatencyMs(50.0);
        sla.setMaxErrorRate(0.01);

        SlaGrader.SlaVerdict verdict = grader.grade(report(prometheusSummary(40), Duration.ofSeconds(20)), sla);

        assertEquals(SlaGrader.NOT_GRADED, verdict.grade());
        assertEquals(0, verdict.totalChecks());
        assertEquals(List.of("maxP999LatencyMs", "maxErrorRate"), fields(verdict));
    }

    @Test
    void evaluableConstraintsStillGradeNextToUnevaluatedOnes() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxP99LatencyMs(100.0);
        sla.setMaxRtoMs(30_000L);
        sla.setMaxDataLossPercent(0.0);

        SlaGrader.SlaVerdict verdict = grader.grade(report(prometheusSummary(250), Duration.ofSeconds(20)), sla);

        assertEquals("F", verdict.grade(), "p99 at 2.5x the limit is critical");
        assertTrue(verdict.violated());
        assertEquals(2, verdict.totalChecks());
        assertEquals(1, verdict.passedChecks());
        assertEquals("p99LatencyMs", verdict.violations().getFirst().metricName());
        assertEquals(List.of("maxDataLossPercent"), fields(verdict));
    }

    @Test
    void constraintsWithNoStepMeasurementAreNotEvaluated() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxP99LatencyMs(100.0);
        sla.setMaxRtoMs(30_000L);

        // Prometheus unreachable and no recovery wait: nothing to compare.
        SlaGrader.SlaVerdict verdict = grader.grade(report(null, null), sla);

        assertEquals(SlaGrader.NOT_GRADED, verdict.grade());
        assertEquals(0, verdict.totalChecks());
        assertEquals(List.of("maxP99LatencyMs", "maxRtoMs"), fields(verdict));
    }

    @Test
    void recoveryTimeIsGradedWithoutPrometheus() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxP99LatencyMs(100.0);
        sla.setMaxRtoMs(30_000L);

        // Recovery comes from the pod watcher; only latency needed Prometheus.
        SlaGrader.SlaVerdict verdict = grader.grade(report(null, Duration.ofSeconds(45)), sla);

        assertEquals(1, verdict.totalChecks());
        assertEquals("rtoMs", verdict.violations().getFirst().metricName());
        assertEquals(List.of("maxP99LatencyMs"), fields(verdict));
    }

    @Test
    void anSlaWithEveryConstraintMetIsStillAnA() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxP99LatencyMs(100.0);
        sla.setMinThroughputRecPerSec(500.0);

        SlaGrader.SlaVerdict verdict = grader.grade(report(prometheusSummary(40), Duration.ofSeconds(20)), sla);

        assertEquals("A", verdict.grade());
        assertEquals(2, verdict.totalChecks());
        assertTrue(verdict.unevaluated().isEmpty());
    }

    @Test
    void averageLatencyIsNotEvaluated() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxAvgLatencyMs(10.0);

        // The capture's average was a mean of the per-quantile gauges divided
        // by 1000; Kafka's exporter publishes no mean to read.
        SlaGrader.SlaVerdict verdict = grader.grade(report(prometheusSummary(40), Duration.ofSeconds(20)), sla);

        assertEquals(SlaGrader.NOT_GRADED, verdict.grade());
        assertEquals(0, verdict.totalChecks());
        assertEquals(List.of("maxAvgLatencyMs"), fields(verdict));
    }

    @Test
    void aMetricPrometheusReturnedNothingForIsNotComparedAgainstZero() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxP99LatencyMs(100.0);
        sla.setMinThroughputRecPerSec(500.0);
        ReportSummary empty = new ReportSummary(0L, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0L, 0, 60_000);

        // Empty results used to read 0: p99 always passed, throughput always
        // failed critically.
        SlaGrader.SlaVerdict verdict = grader.grade(
                report(step(
                        "kill-leader",
                        empty,
                        null,
                        List.of(PrometheusMetricsCapture.P99_LATENCY, PrometheusMetricsCapture.THROUGHPUT),
                        null)),
                sla);

        assertEquals(SlaGrader.NOT_GRADED, verdict.grade());
        assertEquals(0, verdict.totalChecks());
        assertEquals(List.of("maxP99LatencyMs", "minThroughputRecPerSec"), fields(verdict));
        assertTrue(
                verdict.unevaluated().getFirst().contains("Prometheus returned no data"),
                verdict.unevaluated().getFirst());
    }

    @Test
    void aStepWhosePodsNeverCameBackFailsTheRecoveryTime() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxRtoMs(60_000L);

        // No time-to-all-ready, so this used to add no check and grade "-".
        SlaGrader.SlaVerdict verdict =
                grader.grade(report(step("kill-leader", null, null, null, Duration.ofSeconds(330))), sla);

        assertEquals("F", verdict.grade());
        assertEquals(1, verdict.totalChecks());
        SlaGrader.SlaViolation miss = verdict.violations().getFirst();
        assertEquals("rtoMs", miss.metricName());
        assertEquals(330_000, miss.actual());
        assertEquals("CRITICAL", miss.severity());
        assertTrue(verdict.unevaluated().isEmpty());
    }

    @Test
    void anUnrecoveredStepCutShortOfTheLimitIsNotEvaluated() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxRtoMs(600_000L);

        // Kates stopped waiting at 330s; the limit is 600s, so the step could
        // still have recovered in time.
        SlaGrader.SlaVerdict verdict =
                grader.grade(report(step("kill-leader", null, null, null, Duration.ofSeconds(330))), sla);

        assertEquals(SlaGrader.NOT_GRADED, verdict.grade());
        assertEquals(List.of("maxRtoMs"), fields(verdict));
        assertTrue(
                verdict.unevaluated().getFirst().contains("step 'kill-leader'"),
                verdict.unevaluated().getFirst());
    }

    @Test
    void anUnrecoveredStepIsCheckedNextToARecoveredOne() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxRtoMs(60_000L);

        SlaGrader.SlaVerdict verdict = grader.grade(
                report(
                        step("kill-first", null, Duration.ofSeconds(20), null, null),
                        step("kill-second", null, null, null, Duration.ofSeconds(330))),
                sla);

        // The recovered step alone used to grade the plan A.
        assertEquals("F", verdict.grade());
        assertEquals(2, verdict.totalChecks());
        assertEquals(1, verdict.passedChecks());
    }

    @Test
    void noSlaIsUnchanged() {
        SlaGrader.SlaVerdict verdict = grader.grade(report(prometheusSummary(40), null), null);

        assertEquals("A", verdict.grade());
        assertTrue(verdict.unevaluated().isEmpty());
    }
}
