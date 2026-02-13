package com.klster.kates.domain;

import com.fasterxml.jackson.annotation.JsonInclude;

/**
 * A single SLA constraint violation with the metric name,
 * its threshold, the actual observed value, and severity.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public record SlaViolation(
        String metric,
        double threshold,
        double actual,
        Severity severity
) {
    public enum Severity {
        WARNING, CRITICAL
    }

    public static SlaViolation warning(String metric, double threshold, double actual) {
        return new SlaViolation(metric, threshold, actual, Severity.WARNING);
    }

    public static SlaViolation critical(String metric, double threshold, double actual) {
        return new SlaViolation(metric, threshold, actual, Severity.CRITICAL);
    }
}
