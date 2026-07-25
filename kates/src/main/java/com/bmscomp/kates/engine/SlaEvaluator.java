package com.bmscomp.kates.engine;

import java.util.ArrayList;
import java.util.List;
import jakarta.enterprise.context.ApplicationScoped;

import com.bmscomp.kates.domain.SlaDefinition;
import com.bmscomp.kates.domain.SlaMetrics;
import com.bmscomp.kates.domain.SlaVerdict;
import com.bmscomp.kates.domain.SlaViolation;

/**
 * Evaluates a {@link SlaDefinition} against observed metrics and produces a
 * {@link SlaVerdict} with detailed violations.
 *
 * <p>All threshold logic lives here so the live-status path and the report path
 * cannot drift apart.
 */
@ApplicationScoped
public class SlaEvaluator {

    /** Evaluates against a live task status. */
    public SlaVerdict evaluate(SlaDefinition sla, BenchmarkStatus status) {
        if (status == null) {
            return SlaVerdict.pass();
        }
        return evaluate(
                sla,
                new SlaMetrics(
                        status.getP99LatencyMs(),
                        status.getP999LatencyMs(),
                        status.getAvgLatencyMs(),
                        status.getThroughputRecordsPerSec(),
                        status.getRecordsProcessed(),
                        // A task status carries no error count; -1 means "unknown"
                        // so the error-rate constraint is skipped rather than
                        // silently passing as if there were zero errors.
                        -1));
    }

    /** Evaluates against aggregated metrics (report or phase summary). */
    public SlaVerdict evaluate(SlaDefinition sla, SlaMetrics metrics) {
        if (sla == null || !sla.hasConstraints() || metrics == null) {
            return SlaVerdict.pass();
        }

        List<SlaViolation> violations = new ArrayList<>();

        if (sla.getMaxP99LatencyMs() != null && metrics.p99LatencyMs() > sla.getMaxP99LatencyMs()) {
            violations.add(SlaViolation.critical("p99LatencyMs", sla.getMaxP99LatencyMs(), metrics.p99LatencyMs()));
        }

        if (sla.getMaxP999LatencyMs() != null && metrics.p999LatencyMs() > sla.getMaxP999LatencyMs()) {
            violations.add(SlaViolation.critical("p999LatencyMs", sla.getMaxP999LatencyMs(), metrics.p999LatencyMs()));
        }

        if (sla.getMaxAvgLatencyMs() != null && metrics.avgLatencyMs() > sla.getMaxAvgLatencyMs()) {
            violations.add(SlaViolation.warning("avgLatencyMs", sla.getMaxAvgLatencyMs(), metrics.avgLatencyMs()));
        }

        if (sla.getMinThroughputRecPerSec() != null
                && metrics.throughputRecPerSec() < sla.getMinThroughputRecPerSec()) {
            violations.add(SlaViolation.critical(
                    "throughputRecPerSec", sla.getMinThroughputRecPerSec(), metrics.throughputRecPerSec()));
        }

        if (sla.getMinRecordsProcessed() != null && metrics.recordsProcessed() < sla.getMinRecordsProcessed()) {
            violations.add(
                    SlaViolation.warning("recordsProcessed", sla.getMinRecordsProcessed(), metrics.recordsProcessed()));
        }

        // maxErrorRate was a declared constraint that nothing ever checked.
        // Skipped when the caller cannot supply an error rate (negative).
        if (sla.getMaxErrorRate() != null && metrics.errorRate() >= 0 && metrics.errorRate() > sla.getMaxErrorRate()) {
            violations.add(SlaViolation.critical("errorRate", sla.getMaxErrorRate(), metrics.errorRate()));
        }

        if (violations.isEmpty()) {
            return SlaVerdict.pass();
        }
        return SlaVerdict.fail(violations);
    }
}
