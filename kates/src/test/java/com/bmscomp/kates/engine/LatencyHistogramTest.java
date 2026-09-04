package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;

import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;

import io.micrometer.core.instrument.Metrics;
import io.micrometer.core.instrument.simple.SimpleMeterRegistry;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

class LatencyHistogramTest {

    private static final AtomicInteger IDS = new AtomicInteger();

    private LatencyHistogram histogram;

    @BeforeEach
    void setUp() {
        histogram = new LatencyHistogram("test-" + IDS.incrementAndGet());
    }

    @AfterEach
    void tearDown() {
        histogram.close();
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
        assertEquals(42.0, histogram.getMax(), 0.1);

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
        assertEquals(20.0, histogram.getMean(), 0.1);
    }

    @Test
    void maxTracked() {
        histogram.recordLatency(5.0);
        histogram.recordLatency(100.0);
        histogram.recordLatency(50.0);

        assertEquals(100.0, histogram.getMax(), 0.1);
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
        assertEquals(15_000.0, histogram.getMax(), 15.0);
    }

    /**
     * Pins P1-3: histograms with distinct ids must export SEPARATE gauges.
     * Every worker used to construct this with the shared id "global", and
     * Micrometer dedups on name+tags — so only the first run in the JVM was ever
     * exported and every later run's percentiles silently vanished.
     */
    @Test
    void distinctIdsExportDistinctGauges() {
        SimpleMeterRegistry registry = new SimpleMeterRegistry();
        Metrics.addRegistry(registry);
        try (LatencyHistogram first = new LatencyHistogram("run-a");
                LatencyHistogram second = new LatencyHistogram("run-b")) {
            first.recordLatency(10.0);
            second.recordLatency(500.0);

            Double a = registry.find("kates.latency.p99")
                    .tag("id", "run-a")
                    .gauge()
                    .value();
            Double b = registry.find("kates.latency.p99")
                    .tag("id", "run-b")
                    .gauge()
                    .value();

            assertNotNull(a, "run-a must export its own p99");
            assertNotNull(b, "run-b must export its own p99");
            assertTrue(b > a, "each run reports its OWN latency, not the first one registered");
        } finally {
            Metrics.removeRegistry(registry);
        }
    }

    @Test
    void closeUnregistersGauges() {
        SimpleMeterRegistry registry = new SimpleMeterRegistry();
        Metrics.addRegistry(registry);
        try {
            LatencyHistogram scoped = new LatencyHistogram("run-closing");
            scoped.recordLatency(5.0);
            assertNotNull(
                    registry.find("kates.latency.p50").tag("id", "run-closing").gauge(),
                    "gauge registered while the run is live");

            scoped.close();

            assertNull(
                    registry.find("kates.latency.p50").tag("id", "run-closing").gauge(),
                    "gauges removed on close so unbounded run ids cannot grow the registry");
            // Idempotent: terminal paths may close more than once.
            scoped.close();
        } finally {
            Metrics.removeRegistry(registry);
        }
    }

    @Test
    void samplesBeyondTheTrackedRangeAreCounted() {
        LatencyHistogram histogram = new LatencyHistogram("run-clamped");

        histogram.recordLatency(10.0);
        assertEquals(0, histogram.clampedSampleCount());

        // 60s is the ceiling; a five-minute stall is recorded AT it, which makes
        // max and p999 look far healthier than the run actually was.
        histogram.recordLatency(300_000.0);
        histogram.recordLatency(120_000.0);

        assertEquals(2, histogram.clampedSampleCount(), "clamped samples must be visible, not silent");
        assertTrue(histogram.getMax() > 0);

        histogram.reset();
        assertEquals(0, histogram.clampedSampleCount(), "reset clears the clamp count with everything else");
    }
}
