package com.klster.kates.domain;

import java.util.List;

import com.fasterxml.jackson.annotation.JsonInclude;

/**
 * Result of evaluating an {@link SlaDefinition} against observed metrics.
 * Contains the overall pass/fail verdict and a list of individual violations.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public record SlaVerdict(boolean passed, List<SlaViolation> violations) {
    public static SlaVerdict pass() {
        return new SlaVerdict(true, List.of());
    }

    public static SlaVerdict fail(List<SlaViolation> violations) {
        return new SlaVerdict(false, violations);
    }

    public boolean hasCritical() {
        return violations.stream().anyMatch(v -> v.severity() == SlaViolation.Severity.CRITICAL);
    }
}
