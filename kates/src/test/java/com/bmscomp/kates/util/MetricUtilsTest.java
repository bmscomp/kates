package com.bmscomp.kates.util;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;

import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.report.ReportSummary;

class MetricUtilsTest {

    @Test
    void pctChangeEqualValues() {
        assertEquals(0.0, MetricUtils.pctChange(100, 100));
    }

    @Test
    void pctChangeIncrease() {
        assertEquals(50.0, MetricUtils.pctChange(100, 150), 0.001);
    }

    @Test
    void pctChangeDecrease() {
        assertEquals(-25.0, MetricUtils.pctChange(200, 150), 0.001);
    }

    @Test
    void pctChangeZeroBaseWithNonZeroCurrent() {
        assertEquals(100.0, MetricUtils.pctChange(0, 42));
    }

    @Test
    void pctChangeZeroBaseAndZeroCurrent() {
        assertEquals(0.0, MetricUtils.pctChange(0, 0));
    }

    @Test
    void computeSummaryNullReturnsEmpty() {
        ReportSummary s = MetricUtils.computeSummary(null);
        assertEquals(0, s.totalRecords());
        assertEquals(0.0, s.avgThroughputRecPerSec());
    }

    @Test
    void computeSummaryEmptyListReturnsEmpty() {
        ReportSummary s = MetricUtils.computeSummary(List.of());
        assertEquals(0, s.totalRecords());
        assertEquals(0.0, s.errorRate());
    }

    @Test
    void computeSummarySingleResult() {
        TestResult r = new TestResult()
                .withRecordsSent(1000)
                .withThroughputRecordsPerSec(500.0)
                .withThroughputMBPerSec(5.0)
                .withAvgLatencyMs(10.0)
                .withP50LatencyMs(8.0)
                .withP95LatencyMs(15.0)
                .withP99LatencyMs(20.0)
                .withMaxLatencyMs(50.0);

        ReportSummary s = MetricUtils.computeSummary(List.of(r));
        assertEquals(1000, s.totalRecords());
        assertEquals(500.0, s.avgThroughputRecPerSec(), 0.001);
        assertEquals(500.0, s.peakThroughputRecPerSec(), 0.001);
        assertEquals(10.0, s.avgLatencyMs(), 0.001);
        assertEquals(50.0, s.maxLatencyMs(), 0.001);
        assertEquals(0, s.totalErrors());
    }

    @Test
    void computeSummaryMultipleResultsAggregates() {
        TestResult r1 = new TestResult()
                .withRecordsSent(500)
                .withThroughputRecordsPerSec(200.0)
                .withThroughputMBPerSec(2.0)
                .withAvgLatencyMs(10.0)
                .withP50LatencyMs(8.0)
                .withP95LatencyMs(15.0)
                .withP99LatencyMs(18.0)
                .withMaxLatencyMs(30.0);

        TestResult r2 = new TestResult()
                .withRecordsSent(500)
                .withThroughputRecordsPerSec(400.0)
                .withThroughputMBPerSec(4.0)
                .withAvgLatencyMs(20.0)
                .withP50LatencyMs(16.0)
                .withP95LatencyMs(25.0)
                .withP99LatencyMs(28.0)
                .withMaxLatencyMs(60.0);

        ReportSummary s = MetricUtils.computeSummary(List.of(r1, r2));
        assertEquals(1000, s.totalRecords());
        assertEquals(300.0, s.avgThroughputRecPerSec(), 0.001); // average
        assertEquals(400.0, s.peakThroughputRecPerSec(), 0.001); // max
        assertEquals(15.0, s.avgLatencyMs(), 0.001); // average
        assertEquals(60.0, s.maxLatencyMs(), 0.001); // max
    }

    private static TestResult producer(long records, double avg, double p50, double p95, double p99, double max) {
        return new TestResult()
                .withPhaseName("produce")
                .withRecordsSent(records)
                .withAvgLatencyMs(avg)
                .withP50LatencyMs(p50)
                .withP95LatencyMs(p95)
                .withP99LatencyMs(p99)
                .withMaxLatencyMs(max);
    }

    /** A native consumer: it records no latency sample, so every figure is 0. */
    private static TestResult nativeConsumer(long records) {
        return new TestResult().withPhaseName("consume").withRecordsSent(records);
    }

    @Test
    void loadRunLatencyIsTheProducersNotHalvedByTheConsumer() {
        ReportSummary s = MetricUtils.computeSummary(
                List.of(producer(10_000, 12.0, 8.0, 25.0, 40.0, 95.0), nativeConsumer(10_000)));

        assertEquals(40.0, s.p99LatencyMs(), 0.001);
        assertEquals(25.0, s.p95LatencyMs(), 0.001);
        assertEquals(8.0, s.p50LatencyMs(), 0.001);
        assertEquals(12.0, s.avgLatencyMs(), 0.001);
        assertEquals(95.0, s.maxLatencyMs(), 0.001);
    }

    @Test
    void consumerRowOrderDoesNotMatter() {
        ReportSummary s = MetricUtils.computeSummary(
                List.of(nativeConsumer(10_000), producer(10_000, 12.0, 8.0, 25.0, 40.0, 95.0)));

        assertEquals(40.0, s.p99LatencyMs(), 0.001);
        assertEquals(12.0, s.avgLatencyMs(), 0.001);
    }

    @Test
    void trogdorConsumerPollTimeIsNotTheRunsLatency() {
        // Trogdor's consume bench records how long each poll took, not a
        // record's latency; it must not stand in for the producer's figures.
        TestResult trogdorConsumer = new TestResult()
                .withPhaseName("consume")
                .withRecordsSent(10_000)
                .withAvgLatencyMs(300.0)
                .withP50LatencyMs(250.0)
                .withP95LatencyMs(480.0)
                .withP99LatencyMs(500.0);

        ReportSummary s =
                MetricUtils.computeSummary(List.of(producer(10_000, 12.0, 8.0, 25.0, 40.0, 0.0), trogdorConsumer));

        assertEquals(40.0, s.p99LatencyMs(), 0.001);
        assertEquals(12.0, s.avgLatencyMs(), 0.001);
    }

    @Test
    void severalProducersTakeTheHighestPercentileAndARecordWeightedAverage() {
        // A mean of percentiles is not a percentile. The highest is an upper
        // bound on the merged one: every row has 99% of its samples at or
        // under its own P99, so 99% of all samples are under the highest.
        ReportSummary s = MetricUtils.computeSummary(
                List.of(producer(3_000, 10.0, 5.0, 20.0, 30.0, 70.0), producer(1_000, 30.0, 15.0, 45.0, 60.0, 90.0)));

        assertEquals(60.0, s.p99LatencyMs(), 0.001);
        assertEquals(45.0, s.p95LatencyMs(), 0.001);
        assertEquals(15.0, s.p50LatencyMs(), 0.001);
        assertEquals(90.0, s.maxLatencyMs(), 0.001);
        // (3,000 × 10 + 1,000 × 30) / 4,000
        assertEquals(15.0, s.avgLatencyMs(), 0.001);
    }

    @Test
    void producerWithoutASampleDoesNotPullTheAverageDown() {
        // Every send rejected: records were sent, none was acknowledged.
        TestResult rejected = new TestResult()
                .withPhaseName("produce")
                .withRecordsSent(10_000)
                .withError("NOT_ENOUGH_REPLICAS");

        ReportSummary s = MetricUtils.computeSummary(List.of(producer(10_000, 12.0, 8.0, 25.0, 40.0, 95.0), rejected));

        assertEquals(12.0, s.avgLatencyMs(), 0.001);
        assertEquals(40.0, s.p99LatencyMs(), 0.001);
    }

    @Test
    void rowsStoredWithoutPhaseNamesStillIgnoreTheConsumer() {
        // Runs stored before ordinary tasks had phase names: the consumer is
        // told apart by reporting no latency at all.
        TestResult oldProducer = new TestResult()
                .withRecordsSent(10_000)
                .withAvgLatencyMs(12.0)
                .withP99LatencyMs(40.0)
                .withMaxLatencyMs(95.0);
        TestResult oldConsumer = new TestResult().withRecordsSent(10_000);

        ReportSummary s = MetricUtils.computeSummary(List.of(oldProducer, oldConsumer));

        assertEquals(40.0, s.p99LatencyMs(), 0.001);
        assertEquals(12.0, s.avgLatencyMs(), 0.001);
    }

    @Test
    void aListOfConsumersAloneKeepsWhatTheyMeasured() {
        // A LOAD run's "consume" phase summary has nothing else to describe.
        TestResult trogdorConsumer = new TestResult()
                .withPhaseName("consume")
                .withRecordsSent(10_000)
                .withAvgLatencyMs(3.0)
                .withP99LatencyMs(9.0);

        ReportSummary s = MetricUtils.computeSummary(List.of(trogdorConsumer));

        assertEquals(9.0, s.p99LatencyMs(), 0.001);
        assertEquals(3.0, s.avgLatencyMs(), 0.001);
    }

    @Test
    void noRowWithLatencyGivesZeroLatency() {
        ReportSummary s = MetricUtils.computeSummary(List.of(nativeConsumer(10_000)));

        assertEquals(0.0, s.p99LatencyMs());
        assertEquals(0.0, s.avgLatencyMs());
        assertEquals(0.0, s.maxLatencyMs());
        assertEquals(10_000, s.totalRecords());
    }

    @Test
    void latencyIsMeasuredOnlyWhenARowReportsSome() {
        assertTrue(MetricUtils.measuredLatency(
                List.of(producer(10_000, 12.0, 8.0, 25.0, 40.0, 95.0), nativeConsumer(10_000))));

        // A native consumer alone, a producer rejected before its first
        // acknowledgement, no row at all: the summary's 0s are not latencies.
        TestResult rejected = new TestResult()
                .withPhaseName("produce")
                .withRecordsSent(10_000)
                .withError("NOT_ENOUGH_REPLICAS");
        assertFalse(MetricUtils.measuredLatency(List.of(nativeConsumer(10_000))));
        assertFalse(MetricUtils.measuredLatency(List.of(rejected, nativeConsumer(10_000))));
        assertFalse(MetricUtils.measuredLatency(List.of()));
        assertFalse(MetricUtils.measuredLatency(null));
    }

    @Test
    void throughputStillAveragesEveryRow() {
        TestResult produce = producer(10_000, 12.0, 8.0, 25.0, 40.0, 95.0).withThroughputRecordsPerSec(1_000.0);
        TestResult consume = nativeConsumer(10_000).withThroughputRecordsPerSec(900.0);

        ReportSummary s = MetricUtils.computeSummary(List.of(produce, consume));

        assertEquals(950.0, s.avgThroughputRecPerSec(), 0.001);
        assertEquals(1_000.0, s.peakThroughputRecPerSec(), 0.001);
        assertEquals(20_000, s.totalRecords());
    }

    @Test
    void computeSummaryCountsErrors() {
        TestResult ok = new TestResult().withRecordsSent(100);

        TestResult failed = new TestResult().withRecordsSent(100).withError("timeout");

        ReportSummary s = MetricUtils.computeSummary(List.of(ok, failed));
        assertEquals(1, s.totalErrors());
        assertTrue(s.errorRate() > 0);
    }
}
