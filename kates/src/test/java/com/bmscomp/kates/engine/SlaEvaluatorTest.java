package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;

import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.SlaDefinition;
import com.bmscomp.kates.domain.SlaMetrics;
import com.bmscomp.kates.domain.SlaVerdict;
import com.bmscomp.kates.domain.SlaViolation;
import com.bmscomp.kates.domain.TestResult.TaskStatus;

class SlaEvaluatorTest {

    private final SlaEvaluator evaluator = new SlaEvaluator();

    /** Metrics carrying resilience values; latency/throughput are comfortably inside any limit. */
    private static SlaMetrics resilienceMetrics(double dataLossPercent, double maxRtoMs, double rpoMs) {
        return new SlaMetrics(1, 1, 1, 1_000_000, 1_000_000, 0, dataLossPercent, maxRtoMs, rpoMs);
    }

    @Test
    void dataLossBeyondLimitFails() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxDataLossPercent(0.1);

        SlaVerdict verdict = evaluator.evaluate(sla, resilienceMetrics(2.5, -1, -1));

        assertFalse(verdict.passed(), "an SLA declaring only data loss used to pass by construction");
        assertEquals("dataLossPercent", verdict.violations().getFirst().metric());
    }

    @Test
    void rtoBeyondLimitFails() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxRtoMs(5_000L);

        SlaVerdict verdict = evaluator.evaluate(sla, resilienceMetrics(-1, 30_000, -1));

        assertFalse(verdict.passed());
        assertEquals("maxRtoMs", verdict.violations().getFirst().metric());
    }

    @Test
    void rpoBeyondLimitFails() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxRpoMs(1_000L);

        SlaVerdict verdict = evaluator.evaluate(sla, resilienceMetrics(-1, -1, 4_200));

        assertFalse(verdict.passed());
        assertEquals("rpoMs", verdict.violations().getFirst().metric());
    }

    @Test
    void resilienceConstraintsWithinLimitsPass() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxDataLossPercent(1.0);
        sla.setMaxRtoMs(30_000L);
        sla.setMaxRpoMs(5_000L);

        assertTrue(evaluator.evaluate(sla, resilienceMetrics(0.0, 12_000, 900)).passed());
    }

    @Test
    void unknownResilienceMetricsSkipTheConstraint() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxDataLossPercent(0.0);
        sla.setMaxRtoMs(1L);
        sla.setMaxRpoMs(1L);

        // A run with no integrity check reports -1 for all three: unknown, which
        // must skip the check rather than count as either pass or fail evidence.
        SlaVerdict verdict = evaluator.evaluate(sla, resilienceMetrics(-1, -1, -1));

        assertTrue(verdict.passed());
        assertTrue(verdict.violations().isEmpty());
    }

    @Test
    void liveStatusWithoutIntegrityDoesNotBreachResilienceLimits() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxRtoMs(1L);

        BenchmarkStatus status = BenchmarkStatus.builder(TaskStatus.RUNNING).build();

        assertTrue(evaluator.evaluate(sla, status).passed());
    }

    @Test
    void nullSlaReturnsPass() {
        BenchmarkStatus status = BenchmarkStatus.builder(TaskStatus.DONE).build();
        SlaVerdict verdict = evaluator.evaluate(null, status);
        assertTrue(verdict.passed());
        assertTrue(verdict.violations().isEmpty());
    }

    @Test
    void emptySlaReturnsPass() {
        SlaDefinition sla = new SlaDefinition();
        BenchmarkStatus status =
                BenchmarkStatus.builder(TaskStatus.DONE).p99LatencyMs(100).build();
        SlaVerdict verdict = evaluator.evaluate(sla, status);
        assertTrue(verdict.passed());
    }

    @Test
    void allMetricsWithinLimitsReturnsPass() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxP99LatencyMs(50.0);
        sla.setMaxAvgLatencyMs(20.0);
        sla.setMinThroughputRecPerSec(1000.0);
        sla.setMinRecordsProcessed(500L);

        BenchmarkStatus status = BenchmarkStatus.builder(TaskStatus.DONE)
                .p99LatencyMs(40)
                .avgLatencyMs(10)
                .throughputRecordsPerSec(2000)
                .recordsProcessed(1000)
                .build();

        SlaVerdict verdict = evaluator.evaluate(sla, status);
        assertTrue(verdict.passed());
        assertTrue(verdict.violations().isEmpty());
    }

    @Test
    void p99ViolationReturnsCriticalFail() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxP99LatencyMs(50.0);

        BenchmarkStatus status =
                BenchmarkStatus.builder(TaskStatus.DONE).p99LatencyMs(75).build();

        SlaVerdict verdict = evaluator.evaluate(sla, status);
        assertFalse(verdict.passed());
        assertEquals(1, verdict.violations().size());

        SlaViolation v = verdict.violations().get(0);
        assertEquals("p99LatencyMs", v.metric());
        assertEquals(50.0, v.threshold());
        assertEquals(75.0, v.actual());
        assertEquals(SlaViolation.Severity.CRITICAL, v.severity());
    }

    @Test
    void p999ViolationReturnsCriticalFail() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxP999LatencyMs(100.0);

        BenchmarkStatus status =
                BenchmarkStatus.builder(TaskStatus.DONE).p999LatencyMs(150).build();

        SlaVerdict verdict = evaluator.evaluate(sla, status);
        assertFalse(verdict.passed());
        assertTrue(verdict.hasCritical());
        assertEquals("p999LatencyMs", verdict.violations().get(0).metric());
    }

    @Test
    void avgLatencyViolationReturnsWarning() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxAvgLatencyMs(10.0);

        BenchmarkStatus status =
                BenchmarkStatus.builder(TaskStatus.DONE).avgLatencyMs(15).build();

        SlaVerdict verdict = evaluator.evaluate(sla, status);
        assertFalse(verdict.passed());
        assertEquals(SlaViolation.Severity.WARNING, verdict.violations().get(0).severity());
    }

    @Test
    void throughputViolationReturnsCritical() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMinThroughputRecPerSec(10000.0);

        BenchmarkStatus status = BenchmarkStatus.builder(TaskStatus.DONE)
                .throughputRecordsPerSec(5000)
                .build();

        SlaVerdict verdict = evaluator.evaluate(sla, status);
        assertFalse(verdict.passed());
        assertEquals(SlaViolation.Severity.CRITICAL, verdict.violations().get(0).severity());
        assertEquals("throughputRecPerSec", verdict.violations().get(0).metric());
    }

    @Test
    void recordsProcessedViolationReturnsWarning() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMinRecordsProcessed(1000L);

        BenchmarkStatus status =
                BenchmarkStatus.builder(TaskStatus.DONE).recordsProcessed(500).build();

        SlaVerdict verdict = evaluator.evaluate(sla, status);
        assertFalse(verdict.passed());
        assertEquals(SlaViolation.Severity.WARNING, verdict.violations().get(0).severity());
        assertEquals("recordsProcessed", verdict.violations().get(0).metric());
    }

    @Test
    void multipleViolationsReturnedInOrder() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxP99LatencyMs(50.0);
        sla.setMaxAvgLatencyMs(10.0);
        sla.setMinThroughputRecPerSec(10000.0);

        BenchmarkStatus status = BenchmarkStatus.builder(TaskStatus.DONE)
                .p99LatencyMs(100)
                .avgLatencyMs(20)
                .throughputRecordsPerSec(5000)
                .build();

        SlaVerdict verdict = evaluator.evaluate(sla, status);
        assertFalse(verdict.passed());
        assertEquals(3, verdict.violations().size());
        assertEquals("p99LatencyMs", verdict.violations().get(0).metric());
        assertEquals("avgLatencyMs", verdict.violations().get(1).metric());
        assertEquals("throughputRecPerSec", verdict.violations().get(2).metric());
    }

    // ── Aggregated-metrics path (used by report generation) ──────────────────

    @Test
    void aggregatedMetricsBreachIsCaught() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxP99LatencyMs(50.0);
        sla.setMinThroughputRecPerSec(10_000.0);

        SlaMetrics metrics = SlaMetrics.of(120.0, 200.0, 15.0, 4_000.0, 1_000L, 0.0);

        SlaVerdict verdict = evaluator.evaluate(sla, metrics);
        assertFalse(verdict.passed(), "a report summary breaching its SLA must not report PASSED");
        assertEquals(2, verdict.violations().size());
    }

    @Test
    void aggregatedMetricsWithinLimitsPasses() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxP99LatencyMs(50.0);
        sla.setMinThroughputRecPerSec(1_000.0);

        SlaMetrics metrics = SlaMetrics.of(20.0, 35.0, 5.0, 8_000.0, 100_000L, 0.0);

        assertTrue(evaluator.evaluate(sla, metrics).passed());
    }

    @Test
    void errorRateConstraintIsEnforced() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxErrorRate(0.01);

        SlaVerdict breached = evaluator.evaluate(sla, SlaMetrics.of(1, 1, 1, 1, 1, 0.05));
        assertFalse(breached.passed());
        assertEquals("errorRate", breached.violations().get(0).metric());

        assertTrue(evaluator.evaluate(sla, SlaMetrics.of(1, 1, 1, 1, 1, 0.001)).passed());
    }

    @Test
    void unknownErrorRateSkipsTheConstraint() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxErrorRate(0.01);

        // A live BenchmarkStatus carries no error count. "Unknown" must not be
        // graded as a breach, nor silently treated as zero errors.
        BenchmarkStatus status = BenchmarkStatus.builder(TaskStatus.DONE).build();
        assertTrue(evaluator.evaluate(sla, status).passed());
        assertTrue(evaluator.evaluate(sla, SlaMetrics.of(1, 1, 1, 1, 1, -1)).passed());
    }
}
