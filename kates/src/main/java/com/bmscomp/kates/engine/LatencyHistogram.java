package com.bmscomp.kates.engine;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.function.ToDoubleFunction;

import io.micrometer.core.instrument.Gauge;
import io.micrometer.core.instrument.Meter;
import io.micrometer.core.instrument.Metrics;
import io.micrometer.core.instrument.Tags;
import org.HdrHistogram.Histogram;
import org.HdrHistogram.Recorder;

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

    private static final long LOWEST_TRACKABLE_US = 1L;
    private static final long HIGHEST_TRACKABLE_US = 60_000_000L;
    private static final int SIGNIFICANT_DIGITS = 3;

    /**
     * Lock-free recording side. Recording previously took a WRITE lock per
     * sample, so on a multi-producer run every producer serialized through the
     * measurement path and the tool became its own throughput ceiling — it
     * could not measure the loads it induced. {@link Recorder} lets writers
     * record without a lock and hands readers a stable interval snapshot.
     */
    private final Recorder recorder = new Recorder(LOWEST_TRACKABLE_US, HIGHEST_TRACKABLE_US, SIGNIFICANT_DIGITS);

    /** Everything recorded so far, accumulated from drained intervals. */
    private final Histogram cumulative = new Histogram(LOWEST_TRACKABLE_US, HIGHEST_TRACKABLE_US, SIGNIFICANT_DIGITS);

    /** Recycled interval buffer, per HdrHistogram's recommended usage. */
    private Histogram intervalRecycle;

    /**
     * Guards the READ side only (drain + cumulative). Reads happen on the
     * status-poll path, not per record, so contention here is irrelevant.
     */
    private final Object readLock = new Object();

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

    /** Hot path: no lock taken. */
    public void recordLatency(double latencyMs) {
        long latencyUs = (long) (Math.max(0, latencyMs) * 1000.0);
        recorder.recordValue(Math.min(HIGHEST_TRACKABLE_US, Math.max(1, latencyUs)));
    }

    /**
     * Folds everything recorded since the last drain into {@link #cumulative}.
     * Callers must hold {@link #readLock}.
     */
    private void drainIntoCumulative() {
        intervalRecycle = recorder.getIntervalHistogram(intervalRecycle);
        cumulative.add(intervalRecycle);
    }

    public double getPercentile(double percentile) {
        synchronized (readLock) {
            drainIntoCumulative();
            return cumulative.getValueAtPercentile(percentile) / 1000.0;
        }
    }

    public long getTotalCount() {
        synchronized (readLock) {
            drainIntoCumulative();
            return cumulative.getTotalCount();
        }
    }

    public double getMean() {
        synchronized (readLock) {
            drainIntoCumulative();
            return cumulative.getMean() / 1000.0;
        }
    }

    public double getMax() {
        synchronized (readLock) {
            drainIntoCumulative();
            return cumulative.getMaxValue() / 1000.0;
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
        synchronized (readLock) {
            // Drain first so in-flight samples are discarded with everything
            // else instead of surfacing in the next read.
            recorder.reset();
            intervalRecycle = null;
            cumulative.reset();
        }
    }

    public static final double[] HEATMAP_BOUNDARIES = {
        0, 0.5, 1, 2, 3, 5, 7, 10, 15, 20, 30, 50, 75, 100, 150, 200, 300, 500, 750, 1000, 1500, 2000, 3000, 5000, 7500,
        10000
    };

    /** Heatmap buckets over everything recorded so far. */
    public long[] exportBuckets() {
        synchronized (readLock) {
            drainIntoCumulative();
            return bucketize(cumulative);
        }
    }

    /**
     * Heatmap buckets for everything recorded since the last reset, then clears
     * the histogram — the interval a heatmap row represents.
     *
     * <p>Note this also discards the cumulative percentiles, so a caller that
     * polls this on a schedule makes the run's reported latency cover only the
     * final interval. Nothing in the engine calls it today ({@link
     * #exportBuckets()} is used on the poll path); wiring it up should come with
     * a deliberate decision about that trade-off.
     */
    public long[] snapshotAndReset() {
        synchronized (readLock) {
            drainIntoCumulative();
            long[] heatmap = bucketize(cumulative);
            recorder.reset();
            intervalRecycle = null;
            cumulative.reset();
            return heatmap;
        }
    }

    private static long[] bucketize(Histogram source) {
        long[] heatmap = new long[HEATMAP_BOUNDARIES.length - 1];
        for (var iterationValue : source.recordedValues()) {
            double latencyMs = iterationValue.getValueIteratedTo() / 1000.0;
            int target = findHeatmapBucket(latencyMs);
            heatmap[target] += iterationValue.getCountAddedInThisIterationStep();
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
