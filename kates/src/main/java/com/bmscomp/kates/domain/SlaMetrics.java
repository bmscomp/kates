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
        double errorRate) {}
