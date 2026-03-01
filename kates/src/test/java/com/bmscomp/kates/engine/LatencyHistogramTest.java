package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;

import java.util.Map;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

class LatencyHistogramTest {

    private LatencyHistogram histogram;

    @BeforeEach
    void setUp() {
        histogram = new LatencyHistogram();
    }

    @Test
    void emptyHistogramReturnsZeros() {
        assertEquals(0, histogram.getTotalCount());
        assertEquals(0.0, histogram.getMean());
        assertEquals(0.0, histogram.getMax());
        assertEquals(0.0, histogram.getPercentile(50));
        assertEquals(0.0, histogram.getPercentile(99));
    }

    @Test
    void singleValuePercentiles() {
        histogram.recordLatency(42.0);

        assertEquals(1, histogram.getTotalCount());
        assertEquals(42.0, histogram.getMax(), 0.01);

        double p50 = histogram.getPercentile(50);
        double p99 = histogram.getPercentile(99);
        assertEquals(p50, p99, 1.0, "Single value: all percentiles should be roughly equal");
    }

    @Test
    void meanComputedCorrectly() {
        histogram.recordLatency(10.0);
        histogram.recordLatency(20.0);
        histogram.recordLatency(30.0);

        assertEquals(3, histogram.getTotalCount());
        assertEquals(20.0, histogram.getMean(), 0.01);
    }

    @Test
    void maxTracked() {
        histogram.recordLatency(5.0);
        histogram.recordLatency(100.0);
        histogram.recordLatency(50.0);

        assertEquals(100.0, histogram.getMax(), 0.01);
    }

    @Test
    void percentileOrdering() {
        for (int i = 1; i <= 1000; i++) {
            histogram.recordLatency(i * 0.1);
        }

        double p50 = histogram.getPercentile(50);
        double p95 = histogram.getPercentile(95);
        double p99 = histogram.getPercentile(99);
        double p999 = histogram.getPercentile(99.9);

        assertTrue(p50 <= p95, "p50 should be <= p95");
        assertTrue(p95 <= p99, "p95 should be <= p99");
        assertTrue(p99 <= p999, "p99 should be <= p999");
    }

    @Test
    void resetClearsAllState() {
        histogram.recordLatency(10.0);
        histogram.recordLatency(50.0);
        histogram.reset();

        assertEquals(0, histogram.getTotalCount());
        assertEquals(0.0, histogram.getMean());
        assertEquals(0.0, histogram.getMax());
        assertEquals(0.0, histogram.getPercentile(99));
    }

    @Test
    void snapshotContainsExpectedKeys() {
        histogram.recordLatency(5.0);
        Map<String, Double> snap = histogram.snapshot();

        assertTrue(snap.containsKey("mean"));
        assertTrue(snap.containsKey("p50"));
        assertTrue(snap.containsKey("p95"));
        assertTrue(snap.containsKey("p99"));
        assertTrue(snap.containsKey("p999"));
        assertTrue(snap.containsKey("max"));
        assertEquals(6, snap.size());
    }

    @Test
    void exportBucketsLengthMatchesBoundaries() {
        histogram.recordLatency(1.0);
        histogram.recordLatency(100.0);
        histogram.recordLatency(5000.0);

        long[] buckets = histogram.exportBuckets();
        assertEquals(LatencyHistogram.HEATMAP_BOUNDARIES.length - 1, buckets.length);

        long total = 0;
        for (long b : buckets) total += b;
        assertEquals(3, total);
    }

    @Test
    void snapshotAndResetAtomicallyClears() {
        histogram.recordLatency(10.0);
        histogram.recordLatency(20.0);

        long[] heatmap = histogram.snapshotAndReset();

        long total = 0;
        for (long b : heatmap) total += b;
        assertEquals(2, total, "Snapshot should contain all recorded values");

        assertEquals(0, histogram.getTotalCount(), "After snapshotAndReset, count should be 0");
        assertEquals(0.0, histogram.getMean(), "After snapshotAndReset, mean should be 0");
    }

    @Test
    void clampingAtMaxTrackable() {
        histogram.recordLatency(15_000.0);
        assertEquals(1, histogram.getTotalCount());
        assertEquals(15_000.0, histogram.getMax(), 0.01);
    }
}
