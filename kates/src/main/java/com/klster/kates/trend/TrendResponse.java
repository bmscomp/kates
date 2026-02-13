package com.klster.kates.trend;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;

/**
 * Response DTO for historical trend queries.
 * Contains time-series data points, a computed baseline, and detected regressions.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public record TrendResponse(
        String testType,
        String metric,
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
        return new TrendResponse(testType, metric, List.of(), 0, List.of());
    }
}
