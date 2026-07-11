package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import net.jqwik.api.ForAll;
import net.jqwik.api.Property;
import net.jqwik.api.constraints.DoubleRange;
import net.jqwik.api.constraints.IntRange;

import java.util.Map;
import java.util.List;

public class LatencyHistogramPropertyTest {

    @Property
    void validPercentilesAlwaysReturned(@ForAll @DoubleRange(min = -100.0, max = 60_000.0) double latencyMs,
                                        @ForAll @IntRange(min = 1, max = 10_000) int iterations) {
        LatencyHistogram histogram = new LatencyHistogram("test_valid_percentiles");
        for (int i = 0; i < iterations; i++) {
            histogram.recordLatency(latencyMs);
        }

        Map<String, Double> snapshot = histogram.snapshot();
        
        // A latency <= 0 should be clamped to 1us internally, which is 0.001 ms.
        double expectedMin = Math.max(0.001, latencyMs);
        
        // Assert percentiles and max are close to the expected value within HdrHistogram's 3 digit precision.
        assertTrue(snapshot.get("max") >= 0);
        assertTrue(snapshot.get("p99") >= 0);
        
        // If latency is very high, precision allows up to 0.1% error, so check ratio rather than exact absolute delta.
        double delta = Math.max(0.002, expectedMin * 0.005);
        assertEquals(expectedMin, snapshot.get("max"), delta);
        assertEquals(expectedMin, snapshot.get("p50"), delta);
        
        assertEquals(iterations, histogram.getTotalCount());
    }

    @Property
    void heatmapBucketsProperlyTrackAllLatencies(@ForAll List<@DoubleRange(min = -1.0, max = 20000.0) Double> latencies) {
        LatencyHistogram histogram = new LatencyHistogram("test_heatmap");
        for (double l : latencies) {
            histogram.recordLatency(l);
        }

        long[] heatmap = histogram.exportBuckets();
        long sum = 0;
        for (long count : heatmap) {
            sum += count;
        }

        assertEquals(latencies.size(), sum);
        assertEquals(latencies.size(), histogram.getTotalCount());
    }
}
