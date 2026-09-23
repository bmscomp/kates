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

    private static DisruptionReport report(ReportSummary post, Duration timeToAllReady) {
        DisruptionReport report = new DisruptionReport();
        report.setStepReports(List.of(new DisruptionReport.StepReport(
                "kill-leader",
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
                null)));
        return report;
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
    void noSlaIsUnchanged() {
        SlaGrader.SlaVerdict verdict = grader.grade(report(prometheusSummary(40), null), null);

        assertEquals("A", verdict.grade());
        assertTrue(verdict.unevaluated().isEmpty());
    }
}
