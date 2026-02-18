package com.klster.kates.engine;

import java.util.ArrayList;
import java.util.List;
import jakarta.enterprise.context.ApplicationScoped;

import com.klster.kates.domain.SlaDefinition;
import com.klster.kates.domain.SlaVerdict;
import com.klster.kates.domain.SlaViolation;

/**
 * Evaluates a {@link SlaDefinition} against a {@link BenchmarkStatus}
 * and produces a {@link SlaVerdict} with detailed violations.
 */
@ApplicationScoped
public class SlaEvaluator {

    public SlaVerdict evaluate(SlaDefinition sla, BenchmarkStatus status) {
        if (sla == null || !sla.hasConstraints()) {
            return SlaVerdict.pass();
        }

        List<SlaViolation> violations = new ArrayList<>();

        if (sla.getMaxP99LatencyMs() != null && status.getP99LatencyMs() > sla.getMaxP99LatencyMs()) {
            violations.add(SlaViolation.critical("p99LatencyMs", sla.getMaxP99LatencyMs(), status.getP99LatencyMs()));
        }

        if (sla.getMaxP999LatencyMs() != null && status.getP999LatencyMs() > sla.getMaxP999LatencyMs()) {
            violations.add(
                    SlaViolation.critical("p999LatencyMs", sla.getMaxP999LatencyMs(), status.getP999LatencyMs()));
        }

        if (sla.getMaxAvgLatencyMs() != null && status.getAvgLatencyMs() > sla.getMaxAvgLatencyMs()) {
            violations.add(SlaViolation.warning("avgLatencyMs", sla.getMaxAvgLatencyMs(), status.getAvgLatencyMs()));
        }

        if (sla.getMinThroughputRecPerSec() != null
                && status.getThroughputRecordsPerSec() < sla.getMinThroughputRecPerSec()) {
            violations.add(SlaViolation.critical(
                    "throughputRecPerSec", sla.getMinThroughputRecPerSec(), status.getThroughputRecordsPerSec()));
        }

        if (sla.getMinRecordsProcessed() != null && status.getRecordsProcessed() < sla.getMinRecordsProcessed()) {
            violations.add(SlaViolation.warning(
                    "recordsProcessed", sla.getMinRecordsProcessed(), status.getRecordsProcessed()));
        }

        if (violations.isEmpty()) {
            return SlaVerdict.pass();
        }
        return SlaVerdict.fail(violations);
    }
}
