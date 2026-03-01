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

    @Test
    void computeSummaryCountsErrors() {
        TestResult ok = new TestResult()
            .withRecordsSent(100);

        TestResult failed = new TestResult()
            .withRecordsSent(100)
            .withError("timeout");

        ReportSummary s = MetricUtils.computeSummary(List.of(ok, failed));
        assertEquals(1, s.totalErrors());
        assertTrue(s.errorRate() > 0);
    }
}
