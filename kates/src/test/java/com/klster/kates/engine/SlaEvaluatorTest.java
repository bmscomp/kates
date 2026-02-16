package com.klster.kates.engine;

import com.klster.kates.domain.SlaDefinition;
import com.klster.kates.domain.SlaVerdict;
import com.klster.kates.domain.SlaViolation;
import com.klster.kates.domain.TestResult.TaskStatus;
import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.*;

class SlaEvaluatorTest {

    private final SlaEvaluator evaluator = new SlaEvaluator();

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
        BenchmarkStatus status = BenchmarkStatus.builder(TaskStatus.DONE)
                .p99LatencyMs(100).build();
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

        BenchmarkStatus status = BenchmarkStatus.builder(TaskStatus.DONE)
                .p99LatencyMs(75).build();

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

        BenchmarkStatus status = BenchmarkStatus.builder(TaskStatus.DONE)
                .p999LatencyMs(150).build();

        SlaVerdict verdict = evaluator.evaluate(sla, status);
        assertFalse(verdict.passed());
        assertTrue(verdict.hasCritical());
        assertEquals("p999LatencyMs", verdict.violations().get(0).metric());
    }

    @Test
    void avgLatencyViolationReturnsWarning() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxAvgLatencyMs(10.0);

        BenchmarkStatus status = BenchmarkStatus.builder(TaskStatus.DONE)
                .avgLatencyMs(15).build();

        SlaVerdict verdict = evaluator.evaluate(sla, status);
        assertFalse(verdict.passed());
        assertEquals(SlaViolation.Severity.WARNING, verdict.violations().get(0).severity());
    }

    @Test
    void throughputViolationReturnsCritical() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMinThroughputRecPerSec(10000.0);

        BenchmarkStatus status = BenchmarkStatus.builder(TaskStatus.DONE)
                .throughputRecordsPerSec(5000).build();

        SlaVerdict verdict = evaluator.evaluate(sla, status);
        assertFalse(verdict.passed());
        assertEquals(SlaViolation.Severity.CRITICAL, verdict.violations().get(0).severity());
        assertEquals("throughputRecPerSec", verdict.violations().get(0).metric());
    }

    @Test
    void recordsProcessedViolationReturnsWarning() {
        SlaDefinition sla = new SlaDefinition();
        sla.setMinRecordsProcessed(1000L);

        BenchmarkStatus status = BenchmarkStatus.builder(TaskStatus.DONE)
                .recordsProcessed(500).build();

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
}
