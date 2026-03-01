package com.bmscomp.kates.domain;

/**
 * A periodic (typically 1-second) metrics sample recorded during benchmark execution.
 * A sequence of samples forms the time-series for a test run or phase.
 */
public record MetricsSample(
        long timestampMs,
        long recordsInWindow,
        double throughputRecPerSec,
        double throughputMBPerSec,
        double avgLatencyMs,
        double p50LatencyMs,
        double p95LatencyMs,
        double p99LatencyMs,
        double maxLatencyMs,
        long errors) {
    public static MetricsSample of(
            long timestampMs,
            long records,
            double recPerSec,
            double mbPerSec,
            double avgLat,
            double p50,
            double p95,
            double p99,
            double maxLat,
            long errors) {
        return new MetricsSample(timestampMs, records, recPerSec, mbPerSec, avgLat, p50, p95, p99, maxLat, errors);
    }
}
