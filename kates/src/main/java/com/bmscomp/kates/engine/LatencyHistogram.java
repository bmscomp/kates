package com.bmscomp.kates.engine;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.locks.ReentrantReadWriteLock;
import java.util.function.ToDoubleFunction;

import io.micrometer.core.instrument.Gauge;
import io.micrometer.core.instrument.Meter;
import io.micrometer.core.instrument.Metrics;
import io.micrometer.core.instrument.Tags;
import org.HdrHistogram.Histogram;

/**
 * Thread-safe latency recorder backed by an HdrHistogram, exported to
 * Micrometer as p50/p95/p99/p999/max gauges.
 *
 * <p><b>Identity matters.</b> The gauges are tagged {@code id=<id>} and
 * Micrometer deduplicates on name+tags, so every histogram sharing an id maps to
 * ONE meter. When every worker constructed this with the default {@code
 * "global"} id, only the first run of the JVM's lifetime was ever exported —
 * behind a weak reference that went stale once that run was collected, leaving
 * every subsequent run's percentiles invisible. Callers must pass an id unique
 * to the run/task, and {@link #close()} on completion so the series does not
 * accumulate (run ids are unbounded).
 */
public class LatencyHistogram implements AutoCloseable {

    private final Histogram histogram = new Histogram(1L, 60_000_000L, 3);
    private final ReentrantReadWriteLock lock = new ReentrantReadWriteLock();
    private final String id;
    private final List<Meter.Id> meterIds = new ArrayList<>();

    public LatencyHistogram(String id) {
        this.id = id;
        registerGauge("kates.latency.p50", h -> h.getPercentile(50));
        registerGauge("kates.latency.p95", h -> h.getPercentile(95));
        registerGauge("kates.latency.p99", h -> h.getPercentile(99));
        registerGauge("kates.latency.p999", h -> h.getPercentile(99.9));
        registerGauge("kates.latency.max", LatencyHistogram::getMax);
    }

    private void registerGauge(String name, ToDoubleFunction<LatencyHistogram> reader) {
        // Weak reference (Micrometer's default for Gauge.builder) so a missed
        // close() cannot pin the histogram in memory.
        Gauge gauge = Gauge.builder(name, this, reader).tags(Tags.of("id", id)).register(Metrics.globalRegistry);
        meterIds.add(gauge.getId());
    }

    /** The id these gauges are tagged with. */
    public String getId() {
        return id;
    }

    /**
     * Unregisters this histogram's gauges. Idempotent.
     */
    @Override
    public void close() {
        for (Meter.Id meterId : meterIds) {
            Metrics.globalRegistry.remove(meterId);
        }
        meterIds.clear();
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
