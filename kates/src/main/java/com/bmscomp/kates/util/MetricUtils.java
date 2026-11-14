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
     * <p>Throughput is the records the producers sent over the time they ran,
     * first start to last end ({@link #wallClockMs}), and {@code durationMs}
     * is that time; {@code totalRecords} counts the producers' records only
     * ({@link #sendingRows}). A task that has not started is left out of them
     * (see {@link #notStarted}): a scenario's later phase waits for its turn,
     * and counting it understated a running scenario, and the snapshot a
     * resilience run takes before its fault. The latency rows are still chosen
     * from every task, so that a LOAD run whose producer has not started yet
     * does not report its consumer's Trogdor poll times as latency.
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

        List<TestResult> started = results.stream().filter(r -> !notStarted(r)).toList();
        List<TestResult> sending = sendingRows(started);
        long totalRecords =
                sending.stream().mapToLong(TestResult::getRecordsSent).sum();
        long spanMs = wallClockMs(sending);
        double avgThroughput;
        double avgThroughputMB;
        if (spanMs > 0) {
            avgThroughput = totalRecords * 1000.0 / spanMs;
            avgThroughputMB =
                    sending.stream().mapToDouble(MetricUtils::megabytesOf).sum() * 1000.0 / spanMs;
        } else {
            // shortcut: rows stored without a startTime (older runs) keep the mean of their rates, which they were
            // reported with; every row a current Kates API stores has one.
            avgThroughput = sending.stream()
                    .mapToDouble(TestResult::getThroughputRecordsPerSec)
                    .average()
                    .orElse(0);
            avgThroughputMB = sending.stream()
                    .mapToDouble(TestResult::getThroughputMBPerSec)
                    .average()
                    .orElse(0);
        }
        double peakThroughput = results.stream()
                .mapToDouble(TestResult::getThroughputRecordsPerSec)
                .max()
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
        // A share of tasks: a row keeps no count of its rejected sends, and dividing
        // tasks by records let 500,000 of 1,000,000 lost sends read as 1e-6.
        double errorRate = (double) totalErrors / results.size();

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
                Math.max(0, spanMs));
    }

    /**
     * The rows whose records the run sent: the producers. A consumer reads
     * them back, so adding its records counted each one twice and averaging
     * its rate in mixed a read rate into the send rate. Consumer rows alone,
     * such as a LOAD run's "consume" phase, are what there is to describe.
     */
    private static List<TestResult> sendingRows(List<TestResult> rows) {
        List<TestResult> producers = rows.stream().filter(r -> !isConsumer(r)).toList();
        return producers.isEmpty() ? rows : producers;
    }

    /**
     * How long the rows ran, first start to last end, in milliseconds; -1 when
     * a row cannot say. A row's end is its startTime plus its records over its
     * rate, since the rate is records over the time it ran. Rates of tasks
     * running together add up and those of phases taking turns do not, so the
     * run's rate is its records over this span: the mean of five producers at
     * 40,000 records a second read 40,000, not 200,000.
     */
    static long wallClockMs(List<TestResult> rows) {
        long first = Long.MAX_VALUE;
        long last = Long.MIN_VALUE;
        for (TestResult r : rows) {
            if (r.getStartTime() == null || (r.getRecordsSent() > 0 && r.getThroughputRecordsPerSec() <= 0)) {
                return -1;
            }
            long start;
            try {
                start = java.time.Instant.parse(r.getStartTime()).toEpochMilli();
            } catch (java.time.format.DateTimeParseException e) {
                return -1;
            }
            double rate = r.getThroughputRecordsPerSec();
            long end = start + (rate > 0 ? Math.round(r.getRecordsSent() * 1000.0 / rate) : 0);
            first = Math.min(first, start);
            last = Math.max(last, end);
        }
        return rows.isEmpty() ? -1 : last - first;
    }

    /** The megabytes a row moved: its records times the bytes a record its MB/s implies. */
    private static double megabytesOf(TestResult r) {
        double rate = r.getThroughputRecordsPerSec();
        return rate > 0 ? r.getRecordsSent() * r.getThroughputMBPerSec() / rate : 0;
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
