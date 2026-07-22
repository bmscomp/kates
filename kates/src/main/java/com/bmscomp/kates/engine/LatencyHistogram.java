package com.bmscomp.kates.engine;

import java.util.LinkedHashMap;
import java.util.Map;
import java.util.concurrent.locks.ReentrantReadWriteLock;

import io.micrometer.core.instrument.Metrics;
import io.micrometer.core.instrument.Tags;
import org.HdrHistogram.Histogram;

public class LatencyHistogram {

    private final Histogram histogram = new Histogram(1L, 60_000_000L, 3);
    private final ReentrantReadWriteLock lock = new ReentrantReadWriteLock();
    private final String id;

    public LatencyHistogram() {
        this("global");
    }

    public LatencyHistogram(String id) {
        this.id = id;
        Metrics.gauge("kates.latency.p50", Tags.of("id", id), this, h -> h.getPercentile(50));
        Metrics.gauge("kates.latency.p95", Tags.of("id", id), this, h -> h.getPercentile(95));
        Metrics.gauge("kates.latency.p99", Tags.of("id", id), this, h -> h.getPercentile(99));
        Metrics.gauge("kates.latency.p999", Tags.of("id", id), this, h -> h.getPercentile(99.9));
        Metrics.gauge("kates.latency.max", Tags.of("id", id), this, LatencyHistogram::getMax);
    }

    public void recordLatency(double latencyMs) {
        long latencyUs = (long) (Math.max(0, latencyMs) * 1000.0);
        lock.writeLock().lock();
        try {
            histogram.recordValue(Math.max(1, latencyUs));
        } finally {
            lock.writeLock().unlock();
        }
    }

    public double getPercentile(double percentile) {
        lock.readLock().lock();
        try {
            return histogram.getValueAtPercentile(percentile) / 1000.0;
        } finally {
            lock.readLock().unlock();
        }
    }

    public long getTotalCount() {
        lock.readLock().lock();
        try {
            return histogram.getTotalCount();
        } finally {
            lock.readLock().unlock();
        }
    }

    public double getMean() {
        lock.readLock().lock();
        try {
            return histogram.getMean() / 1000.0;
        } finally {
            lock.readLock().unlock();
        }
    }

    public double getMax() {
        lock.readLock().lock();
        try {
            return histogram.getMaxValue() / 1000.0;
        } finally {
            lock.readLock().unlock();
        }
    }

    public Map<String, Double> snapshot() {
        Map<String, Double> result = new LinkedHashMap<>();
        result.put("mean", getMean());
        result.put("p50", getPercentile(50));
        result.put("p95", getPercentile(95));
        result.put("p99", getPercentile(99));
        result.put("p999", getPercentile(99.9));
        result.put("max", getMax());
        return result;
    }

    public void reset() {
        lock.writeLock().lock();
        try {
            histogram.reset();
        } finally {
            lock.writeLock().unlock();
        }
    }

    public static final double[] HEATMAP_BOUNDARIES = {
        0, 0.5, 1, 2, 3, 5, 7, 10, 15, 20, 30, 50, 75, 100, 150, 200, 300, 500, 750, 1000, 1500, 2000, 3000, 5000, 7500,
        10000
    };

    public long[] exportBuckets() {
        int heatmapLen = HEATMAP_BOUNDARIES.length - 1;
        long[] heatmap = new long[heatmapLen];
        lock.readLock().lock();
        try {
            for (var iterationValue : histogram.recordedValues()) {
                double latencyMs = iterationValue.getValueIteratedTo() / 1000.0;
                int target = findHeatmapBucket(latencyMs);
                heatmap[target] += iterationValue.getCountAddedInThisIterationStep();
            }
        } finally {
            lock.readLock().unlock();
        }
        return heatmap;
    }

    public long[] snapshotAndReset() {
        int heatmapLen = HEATMAP_BOUNDARIES.length - 1;
        long[] heatmap = new long[heatmapLen];
        lock.writeLock().lock();
        try {
            for (var iterationValue : histogram.recordedValues()) {
                double latencyMs = iterationValue.getValueIteratedTo() / 1000.0;
                int target = findHeatmapBucket(latencyMs);
                heatmap[target] += iterationValue.getCountAddedInThisIterationStep();
            }
            histogram.reset();
        } finally {
            lock.writeLock().unlock();
        }
        return heatmap;
    }

    private static int findHeatmapBucket(double latencyMs) {
        for (int i = HEATMAP_BOUNDARIES.length - 2; i >= 0; i--) {
            if (latencyMs >= HEATMAP_BOUNDARIES[i]) {
                return i;
            }
        }
        return 0;
    }
}
