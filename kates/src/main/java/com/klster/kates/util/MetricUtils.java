package com.klster.kates.util;

import java.util.List;

import com.klster.kates.domain.TestResult;
import com.klster.kates.report.ReportSummary;

/**
 * Shared metric computation utilities used by report generation,
 * resilience analysis, and trend comparison.
 */
public final class MetricUtils {

    private MetricUtils() {}

    /**
     * Computes the percentage change between a baseline and current value.
     *
     * @return percentage change (positive = increase, negative = decrease)
     */
    public static double pctChange(double base, double current) {
        if (base == 0) return current == 0 ? 0 : 100.0;
        return ((current - base) / base) * 100.0;
    }

    /**
     * Aggregates a list of {@link TestResult}s into a single {@link ReportSummary}.
     * Returns an empty summary when the list is null or empty.
     */
    public static ReportSummary computeSummary(List<TestResult> results) {
        if (results == null || results.isEmpty()) {
            return new ReportSummary(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0);
        }

        long totalRecords =
                results.stream().mapToLong(TestResult::getRecordsSent).sum();
        double avgThroughput = results.stream()
                .mapToDouble(TestResult::getThroughputRecordsPerSec)
                .average()
                .orElse(0);
        double peakThroughput = results.stream()
                .mapToDouble(TestResult::getThroughputRecordsPerSec)
                .max()
                .orElse(0);
        double avgThroughputMB = results.stream()
                .mapToDouble(TestResult::getThroughputMBPerSec)
                .average()
                .orElse(0);
        double avgLatency = results.stream()
                .mapToDouble(TestResult::getAvgLatencyMs)
                .average()
                .orElse(0);
        double p50 = results.stream()
                .mapToDouble(TestResult::getP50LatencyMs)
                .average()
                .orElse(0);
        double p95 = results.stream()
                .mapToDouble(TestResult::getP95LatencyMs)
                .average()
                .orElse(0);
        double p99 = results.stream()
                .mapToDouble(TestResult::getP99LatencyMs)
                .average()
                .orElse(0);
        double maxLatency =
                results.stream().mapToDouble(TestResult::getMaxLatencyMs).max().orElse(0);
        long totalErrors = results.stream().filter(r -> r.getError() != null).count();
        double errorRate = totalRecords > 0 ? (double) totalErrors / totalRecords : 0;

        return new ReportSummary(
                totalRecords,
                avgThroughput,
                peakThroughput,
                avgThroughputMB,
                avgLatency,
                p50,
                p95,
                p99,
                0,
                maxLatency,
                totalErrors,
                errorRate,
                0);
    }
}
