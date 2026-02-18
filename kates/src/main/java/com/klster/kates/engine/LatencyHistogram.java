package com.klster.kates.engine;

import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.locks.ReentrantReadWriteLock;

/**
 * Thread-safe latency histogram that records latency values in microsecond precision
 * and computes accurate percentiles (p50, p95, p99, p99.9).
 *
 * <p>Uses a logarithmic bucketing scheme for memory-efficient storage while
 * maintaining good accuracy across a wide latency range (0–10 seconds).
 */
public class LatencyHistogram {

    private static final int BUCKET_COUNT = 1024;
    private static final double MAX_TRACKABLE_MS = 10_000.0;
    private static final double LOG_BASE = Math.log(MAX_TRACKABLE_MS + 1);

    private final long[] buckets = new long[BUCKET_COUNT];
    private final AtomicLong totalCount = new AtomicLong(0);
    private volatile double maxValue = 0;
    private volatile double sumValue = 0;
    private final ReentrantReadWriteLock lock = new ReentrantReadWriteLock();

    public void recordLatency(double latencyMs) {
        int bucket = toBucket(Math.max(0, Math.min(latencyMs, MAX_TRACKABLE_MS)));
        lock.writeLock().lock();
        try {
            buckets[bucket]++;
            totalCount.incrementAndGet();
            sumValue += latencyMs;
            if (latencyMs > maxValue) {
                maxValue = latencyMs;
            }
        } finally {
            lock.writeLock().unlock();
        }
    }

    public double getPercentile(double percentile) {
        lock.readLock().lock();
        try {
            long total = totalCount.get();
            if (total == 0) return 0;

            long threshold = (long) Math.ceil(total * percentile / 100.0);
            long cumulative = 0;
            for (int i = 0; i < BUCKET_COUNT; i++) {
                cumulative += buckets[i];
                if (cumulative >= threshold) {
                    return fromBucket(i);
                }
            }
            return maxValue;
        } finally {
            lock.readLock().unlock();
        }
    }

    public long getTotalCount() {
        return totalCount.get();
    }

    public double getMean() {
        lock.readLock().lock();
        try {
            long total = totalCount.get();
            return total == 0 ? 0 : sumValue / total;
        } finally {
            lock.readLock().unlock();
        }
    }

    public double getMax() {
        return maxValue;
    }

    /**
     * Returns a named snapshot of common percentiles.
     */
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

    /**
     * Resets all counters. Useful between phases.
     */
    public void reset() {
        lock.writeLock().lock();
        try {
            Arrays.fill(buckets, 0);
            totalCount.set(0);
            maxValue = 0;
            sumValue = 0;
        } finally {
            lock.writeLock().unlock();
        }
    }
    /**
     * 32 logarithmic bucket boundaries (in ms) for heatmap export.
     * Covers 0 → 10,000 ms with increasing granularity at lower latencies.
     */
    public static final double[] HEATMAP_BOUNDARIES = {
        0, 0.5, 1, 2, 3, 5, 7, 10, 15, 20, 30, 50, 75, 100, 150, 200, 300, 500, 750, 1000, 1500, 2000, 3000, 5000, 7500,
        10000
    };

    /**
     * Compresses 1024 internal buckets into {@link #HEATMAP_BOUNDARIES} ranges.
     * Returns an array of length {@code HEATMAP_BOUNDARIES.length - 1} where each
     * entry is the count of latencies falling within that range.
     */
    public long[] exportBuckets() {
        int heatmapLen = HEATMAP_BOUNDARIES.length - 1;
        long[] heatmap = new long[heatmapLen];
        lock.readLock().lock();
        try {
            for (int i = 0; i < BUCKET_COUNT; i++) {
                if (buckets[i] == 0) continue;
                double latencyMs = fromBucket(i);
                int target = findHeatmapBucket(latencyMs);
                heatmap[target] += buckets[i];
            }
        } finally {
            lock.readLock().unlock();
        }
        return heatmap;
    }

    /**
     * Atomically captures the current bucket distribution compressed to heatmap
     * boundaries and resets all counters. Used for windowed collection.
     */
    public long[] snapshotAndReset() {
        int heatmapLen = HEATMAP_BOUNDARIES.length - 1;
        long[] heatmap = new long[heatmapLen];
        lock.writeLock().lock();
        try {
            for (int i = 0; i < BUCKET_COUNT; i++) {
                if (buckets[i] == 0) continue;
                double latencyMs = fromBucket(i);
                int target = findHeatmapBucket(latencyMs);
                heatmap[target] += buckets[i];
            }
            Arrays.fill(buckets, 0);
            totalCount.set(0);
            maxValue = 0;
            sumValue = 0;
        } finally {
            lock.writeLock().unlock();
        }
        return heatmap;
    }

    private int toBucket(double latencyMs) {
        if (latencyMs <= 0) return 0;
        int bucket = (int) (Math.log(latencyMs + 1) / LOG_BASE * (BUCKET_COUNT - 1));
        return Math.min(bucket, BUCKET_COUNT - 1);
    }

    private double fromBucket(int bucket) {
        return Math.exp((double) bucket / (BUCKET_COUNT - 1) * LOG_BASE) - 1;
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
