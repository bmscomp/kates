package com.klster.kates.report;

import com.fasterxml.jackson.annotation.JsonInclude;

/**
 * Aggregated metrics summary computed from a test run or individual phase.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public record ReportSummary(
        long totalRecords,
        double avgThroughputRecPerSec,
        double peakThroughputRecPerSec,
        double avgThroughputMBPerSec,
        double avgLatencyMs,
        double p50LatencyMs,
        double p95LatencyMs,
        double p99LatencyMs,
        double p999LatencyMs,
        double maxLatencyMs,
        long totalErrors,
        double errorRate,
        long durationMs) {}
