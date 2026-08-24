package com.bmscomp.kates.domain;

/**
 * The observed values an {@link SlaDefinition} is evaluated against.
 *
 * <p>Exists so the SLA rules live in exactly one place while being reachable
 * from both sides of the system: the engine evaluates a live
 * {@code BenchmarkStatus}, the report layer evaluates an aggregated
 * {@code ReportSummary}. Passing this neutral carrier keeps the evaluator in
 * {@code engine} free of a dependency back on {@code report}.
 */
public record SlaMetrics(
        double p99LatencyMs,
        double p999LatencyMs,
        double avgLatencyMs,
        double throughputRecPerSec,
        long recordsProcessed,
        /**
         * Fraction in [0,1], or a negative value when the source cannot report
         * it — the evaluator then skips the error-rate constraint rather than
         * treating "unknown" as "zero errors".
         */
        double errorRate,
        /**
         * Percentage of records produced but never consumed, from the integrity
         * verifier. Negative when the run carried no integrity check.
         */
        double dataLossPercent,
        /** Worst observed recovery time in ms; negative when unknown. */
        double maxRtoMs,
        /** Observed recovery point objective in ms; negative when unknown. */
        double rpoMs) {

    /**
     * Metrics with no resilience data — for callers that only observe latency,
     * throughput and errors. The resilience constraints are then skipped rather
     * than passing on absent evidence.
     */
    public static SlaMetrics of(
            double p99LatencyMs,
            double p999LatencyMs,
            double avgLatencyMs,
            double throughputRecPerSec,
            long recordsProcessed,
            double errorRate) {
        return new SlaMetrics(
                p99LatencyMs,
                p999LatencyMs,
                avgLatencyMs,
                throughputRecPerSec,
                recordsProcessed,
                errorRate,
                -1,
                -1,
                -1);
    }
}
