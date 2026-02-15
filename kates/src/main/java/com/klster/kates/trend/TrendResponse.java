package com.klster.kates.trend;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;

/**
 * Response DTO for historical trend queries.
 * Contains time-series data points, a computed baseline, and detected regressions.
 * When {@code phase} is non-null the data was extracted from that specific test phase
 * rather than the overall run summary.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public record TrendResponse(
        String testType,
        String metric,
        String phase,
        List<DataPoint> dataPoints,
        double baseline,
        List<Regression> regressions
) {
    public record DataPoint(String timestamp, String runId, double value) {}

    public record Regression(
            String runId,
            String timestamp,
            double value,
            double baseline,
            double deviationPercent
    ) {}

    public static TrendResponse empty(String testType, String metric) {
        return new TrendResponse(testType, metric, null, List.of(), 0, List.of());
    }

    public static TrendResponse empty(String testType, String metric, String phase) {
        return new TrendResponse(testType, metric, phase, List.of(), 0, List.of());
    }
}
