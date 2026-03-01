package com.bmscomp.kates.trend;

import java.util.List;

import com.fasterxml.jackson.annotation.JsonInclude;

/**
 * Response DTO for phase-level trend breakdown.
 * Contains one {@link PhaseTrend} per distinct phase found across matching test runs,
 * allowing side-by-side comparison of phase behaviour over time.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public record PhaseTrendResponse(String testType, String metric, List<PhaseTrend> phases) {
    public record PhaseTrend(
            String phase,
            List<TrendResponse.DataPoint> dataPoints,
            double baseline,
            List<TrendResponse.Regression> regressions) {}
}
