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
        var integrity = status.getIntegrityResult();
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
                        -1,
                        // Resilience values exist only once an integrity check has
                        // run; -1 again means "unknown", never "clean".
                        integrity != null ? integrity.dataLossPercent() : -1,
                        integrity != null ? integrity.maxRtoMs() : -1,
                        integrity != null ? integrity.rpoMs() : -1));
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

        // Resilience thresholds. These counted towards hasConstraints() but
        // nothing evaluated them, so an SLA declaring ONLY a data-loss, RTO or
        // RPO limit passed by construction — the same green-by-default bug the
        // error-rate check above fixed. Negative observations mean the run
        // carried no integrity check, in which case the constraint is skipped
        // rather than passed.
        if (sla.getMaxDataLossPercent() != null
                && metrics.dataLossPercent() >= 0
                && metrics.dataLossPercent() > sla.getMaxDataLossPercent()) {
            violations.add(
                    SlaViolation.critical("dataLossPercent", sla.getMaxDataLossPercent(), metrics.dataLossPercent()));
        }

        if (sla.getMaxRtoMs() != null && metrics.maxRtoMs() >= 0 && metrics.maxRtoMs() > sla.getMaxRtoMs()) {
            violations.add(SlaViolation.critical("maxRtoMs", sla.getMaxRtoMs(), metrics.maxRtoMs()));
        }

        if (sla.getMaxRpoMs() != null && metrics.rpoMs() >= 0 && metrics.rpoMs() > sla.getMaxRpoMs()) {
            violations.add(SlaViolation.critical("rpoMs", sla.getMaxRpoMs(), metrics.rpoMs()));
        }

        if (violations.isEmpty()) {
            return SlaVerdict.pass();
        }
        return SlaVerdict.fail(violations);
    }
}
