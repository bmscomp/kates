package com.klster.kates.util;

import com.klster.kates.domain.TestResult;
import com.klster.kates.report.ReportSummary;
import org.junit.jupiter.api.Test;

import java.util.List;

import static org.junit.jupiter.api.Assertions.*;

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
        TestResult r = new TestResult();
        r.setRecordsSent(1000);
        r.setThroughputRecordsPerSec(500.0);
        r.setThroughputMBPerSec(5.0);
        r.setAvgLatencyMs(10.0);
        r.setP50LatencyMs(8.0);
        r.setP95LatencyMs(15.0);
        r.setP99LatencyMs(20.0);
        r.setMaxLatencyMs(50.0);

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
        TestResult r1 = new TestResult();
        r1.setRecordsSent(500);
        r1.setThroughputRecordsPerSec(200.0);
        r1.setThroughputMBPerSec(2.0);
        r1.setAvgLatencyMs(10.0);
        r1.setP50LatencyMs(8.0);
        r1.setP95LatencyMs(15.0);
        r1.setP99LatencyMs(18.0);
        r1.setMaxLatencyMs(30.0);

        TestResult r2 = new TestResult();
        r2.setRecordsSent(500);
        r2.setThroughputRecordsPerSec(400.0);
        r2.setThroughputMBPerSec(4.0);
        r2.setAvgLatencyMs(20.0);
        r2.setP50LatencyMs(16.0);
        r2.setP95LatencyMs(25.0);
        r2.setP99LatencyMs(28.0);
        r2.setMaxLatencyMs(60.0);

        ReportSummary s = MetricUtils.computeSummary(List.of(r1, r2));
        assertEquals(1000, s.totalRecords());
        assertEquals(300.0, s.avgThroughputRecPerSec(), 0.001); // average
        assertEquals(400.0, s.peakThroughputRecPerSec(), 0.001); // max
        assertEquals(15.0, s.avgLatencyMs(), 0.001); // average
        assertEquals(60.0, s.maxLatencyMs(), 0.001); // max
    }

    @Test
    void computeSummaryCountsErrors() {
        TestResult ok = new TestResult();
        ok.setRecordsSent(100);

        TestResult failed = new TestResult();
        failed.setRecordsSent(100);
        failed.setError("timeout");

        ReportSummary s = MetricUtils.computeSummary(List.of(ok, failed));
        assertEquals(1, s.totalErrors());
        assertTrue(s.errorRate() > 0);
    }
}
