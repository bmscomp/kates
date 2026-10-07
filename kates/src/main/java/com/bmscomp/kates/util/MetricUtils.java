package com.bmscomp.kates.util;

import java.util.List;
import java.util.function.ToDoubleFunction;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.report.ReportSummary;

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
     *
     * <p>The throughput means leave out a task that has not started (see
     * {@link #notStarted}). A scenario's later phase waits for its turn, and
     * averaging its 0 in understated a running scenario: while the second of
     * two phases waited, the run read half the first phase's rate, and so did
     * the snapshot a resilience run takes before its fault. Only the means
     * leave it out. It adds nothing to the other figures, and the latency rows
     * are still chosen from every task, so that a LOAD run whose producer has
     * not started yet does not report its consumer's Trogdor poll times as
     * latency.
     *
     * <p>The latency figures come only from the rows that measured latency
     * (see {@link #latencyRows}), and they are never averaged. The percentiles
     * used to be the mean of every row's, so a LOAD run's consumer, which
     * records no latency, halved its producer's P99 — and a mean of
     * percentiles is not a percentile of anything even when every row has
     * one. A row keeps five numbers, not its histogram (the backend's
     * HdrHistogram lives only as long as the task), so there is nothing to
     * merge. Instead:
     *
     * <ul>
     *   <li>P50, P95 and P99 are the highest of the latency rows'. With one row
     *       — LOAD, ENDURANCE, ROUND_TRIP, INTEGRITY and every single-producer
     *       type — that is the row's own figure. With several (STRESS or
     *       CAPACITY producers, scenario phases) it is an upper bound on the
     *       run's: when every row has 99% of its samples at or under its own
     *       P99, 99% of all samples are at or under the highest of them.
     *   <li>The average is weighted by each row's records, which is how means
     *       combine.
     *   <li>Max is the highest max.
     * </ul>
     */
    public static ReportSummary computeSummary(List<TestResult> results) {
        if (results == null || results.isEmpty()) {
            return new ReportSummary(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0);
        }

        long totalRecords =
                results.stream().mapToLong(TestResult::getRecordsSent).sum();
        List<TestResult> started = results.stream().filter(r -> !notStarted(r)).toList();
        double avgThroughput = started.stream()
                .mapToDouble(TestResult::getThroughputRecordsPerSec)
                .average()
                .orElse(0);
        double peakThroughput = results.stream()
                .mapToDouble(TestResult::getThroughputRecordsPerSec)
                .max()
                .orElse(0);
        double avgThroughputMB = started.stream()
                .mapToDouble(TestResult::getThroughputMBPerSec)
                .average()
                .orElse(0);

        List<TestResult> measured = latencyRows(results);
        long measuredRecords =
                measured.stream().mapToLong(TestResult::getRecordsSent).sum();
        double avgLatency = measuredRecords > 0
                ? measured.stream()
                                .mapToDouble(r -> r.getAvgLatencyMs() * r.getRecordsSent())
                                .sum()
                        / measuredRecords
                : measured.stream()
                        .mapToDouble(TestResult::getAvgLatencyMs)
                        .average()
                        .orElse(0);
        double p50 = highest(measured, TestResult::getP50LatencyMs);
        double p95 = highest(measured, TestResult::getP95LatencyMs);
        double p99 = highest(measured, TestResult::getP99LatencyMs);
        double maxLatency = highest(measured, TestResult::getMaxLatencyMs);

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

    /**
     * Whether the task has not started: PENDING, with no records and no rate.
     * A scenario's later phase is, until its turn
     * (TestOrchestrator.executeScenario), and so is any task its backend has
     * not started yet. Its rates are 0 because it measured nothing, not
     * because it ran at 0. A row that carries records or a rate measured
     * something, whatever its status, so leaving such rows out drops only 0s.
     * A task a cancel or a failure ends before its turn is stored FAILED, so
     * it is not one either.
     */
    private static boolean notStarted(TestResult result) {
        return result.getStatus() == TestResult.TaskStatus.PENDING
                && result.getRecordsSent() == 0
                && result.getThroughputRecordsPerSec() == 0
                && result.getThroughputMBPerSec() == 0;
    }

    /**
     * The rows whose latency describes the run.
     *
     * <p>A consumer row is left out. On the native backend it records no
     * latency sample, so every figure it carries is 0; on Trogdor it records
     * how long each poll took, which is not a record's latency either. A list
     * of consumer rows alone, such as a LOAD run's "consume" phase, keeps them:
     * there is nothing else to describe. Of what remains, a row that reports
     * no latency at all is left out too — a producer that failed before its
     * first acknowledgement, or a consumer stored before rows had phase names
     * — so that its records cannot pull the average down.
     */
    static List<TestResult> latencyRows(List<TestResult> results) {
        List<TestResult> producers =
                results.stream().filter(r -> !isConsumer(r)).toList();
        List<TestResult> candidates = producers.isEmpty() ? results : producers;
        return candidates.stream().filter(MetricUtils::reportsLatency).toList();
    }

    /**
     * Whether {@link #computeSummary} has any latency to report for these rows.
     * When it has none, every latency figure of the summary is 0, which means
     * "not measured", not a run faster than any gate.
     */
    public static boolean measuredLatency(List<TestResult> results) {
        return results != null && !latencyRows(results).isEmpty();
    }

    /** The phase name TestOrchestrator gives a CONSUME task's row (phaseNameFor). */
    private static boolean isConsumer(TestResult result) {
        return "consume".equals(result.getPhaseName());
    }

    private static boolean reportsLatency(TestResult result) {
        return result.getAvgLatencyMs() > 0
                || result.getP50LatencyMs() > 0
                || result.getP95LatencyMs() > 0
                || result.getP99LatencyMs() > 0
                || result.getMaxLatencyMs() > 0;
    }

    private static double highest(List<TestResult> rows, ToDoubleFunction<TestResult> metric) {
        return rows.stream().mapToDouble(metric).max().orElse(0);
    }
}
