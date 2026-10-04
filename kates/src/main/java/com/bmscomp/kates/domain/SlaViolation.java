package com.bmscomp.kates.domain;

import com.fasterxml.jackson.annotation.JsonInclude;

/**
 * A single SLA constraint violation with the metric name,
 * its threshold, the actual observed value, and severity.
 *
 * <p>{@code reason} is set only when the numbers cannot say what went wrong:
 * a gate whose metric the run did not measure, or a FAILED run. A threshold or
 * actual value the violation does not have is then -1, the "unknown" the rest
 * of the SLA code uses. Without a reason, a violation serializes as it always
 * has.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public record SlaViolation(String metric, double threshold, double actual, Severity severity, String reason) {
    public enum Severity {
        WARNING,
        CRITICAL
    }

    /** The metric whose gate the run's status fails: a FAILED run meets no SLA. */
    public static final String RUN_STATUS = "status";

    public SlaViolation(String metric, double threshold, double actual, Severity severity) {
        this(metric, threshold, actual, severity, null);
    }

    public static SlaViolation warning(String metric, double threshold, double actual) {
        return new SlaViolation(metric, threshold, actual, Severity.WARNING);
    }

    public static SlaViolation critical(String metric, double threshold, double actual) {
        return new SlaViolation(metric, threshold, actual, Severity.CRITICAL);
    }

    /** A gate on a metric the run did not measure, which fails at the gate's own severity. */
    public static SlaViolation notMeasured(String metric, double threshold, Severity severity) {
        return new SlaViolation(metric, threshold, -1, severity, "not measured");
    }

    /** A FAILED run; {@code reason} names its failure, such as "FAILED: NOT_ENOUGH_REPLICAS". */
    public static SlaViolation runFailed(String reason) {
        return new SlaViolation(RUN_STATUS, -1, -1, Severity.CRITICAL, reason);
    }
}
