package com.klster.kates.engine;

import com.klster.kates.domain.IntegrityResult;
import com.klster.kates.domain.MetricsSample;
import com.klster.kates.domain.TestResult.TaskStatus;

import java.util.List;
import java.util.Map;

/**
 * Unified status snapshot returned by {@link BenchmarkBackend#poll}.
 * Backends map their internal state to this common model.
 */
public class BenchmarkStatus {

    private final TaskStatus state;
    private final long recordsProcessed;
    private final double throughputRecordsPerSec;
    private final double throughputMBPerSec;
    private final double avgLatencyMs;
    private final double p50LatencyMs;
    private final double p95LatencyMs;
    private final double p99LatencyMs;
    private final double p999LatencyMs;
    private final double maxLatencyMs;
    private final String error;
    private final IntegrityResult integrityResult;
    private final List<MetricsSample> timeSeries;
    private final Map<String, Double> latencyHistogram;
    private final long[] heatmapBuckets;

    private BenchmarkStatus(Builder builder) {
        this.state = builder.state;
        this.recordsProcessed = builder.recordsProcessed;
        this.throughputRecordsPerSec = builder.throughputRecordsPerSec;
        this.throughputMBPerSec = builder.throughputMBPerSec;
        this.avgLatencyMs = builder.avgLatencyMs;
        this.p50LatencyMs = builder.p50LatencyMs;
        this.p95LatencyMs = builder.p95LatencyMs;
        this.p99LatencyMs = builder.p99LatencyMs;
        this.p999LatencyMs = builder.p999LatencyMs;
        this.maxLatencyMs = builder.maxLatencyMs;
        this.error = builder.error;
        this.integrityResult = builder.integrityResult;
        this.timeSeries = builder.timeSeries != null ? List.copyOf(builder.timeSeries) : List.of();
        this.latencyHistogram = builder.latencyHistogram != null ? Map.copyOf(builder.latencyHistogram) : Map.of();
        this.heatmapBuckets = builder.heatmapBuckets;
    }

    public TaskStatus getState() { return state; }
    public long getRecordsProcessed() { return recordsProcessed; }
    public double getThroughputRecordsPerSec() { return throughputRecordsPerSec; }
    public double getThroughputMBPerSec() { return throughputMBPerSec; }
    public double getAvgLatencyMs() { return avgLatencyMs; }
    public double getP50LatencyMs() { return p50LatencyMs; }
    public double getP95LatencyMs() { return p95LatencyMs; }
    public double getP99LatencyMs() { return p99LatencyMs; }
    public double getP999LatencyMs() { return p999LatencyMs; }
    public double getMaxLatencyMs() { return maxLatencyMs; }
    public String getError() { return error; }
    public IntegrityResult getIntegrityResult() { return integrityResult; }
    public List<MetricsSample> getTimeSeries() { return timeSeries; }
    public Map<String, Double> getLatencyHistogram() { return latencyHistogram; }
    public long[] getHeatmapBuckets() { return heatmapBuckets; }

    public boolean isTerminal() {
        return state == TaskStatus.DONE || state == TaskStatus.FAILED;
    }

    public static Builder builder(TaskStatus state) {
        return new Builder(state);
    }

    public static class Builder {
        private final TaskStatus state;
        private long recordsProcessed;
        private double throughputRecordsPerSec;
        private double throughputMBPerSec;
        private double avgLatencyMs;
        private double p50LatencyMs;
        private double p95LatencyMs;
        private double p99LatencyMs;
        private double p999LatencyMs;
        private double maxLatencyMs;
        private String error;
        private IntegrityResult integrityResult;
        private List<MetricsSample> timeSeries;
        private Map<String, Double> latencyHistogram;
        private long[] heatmapBuckets;

        private Builder(TaskStatus state) {
            this.state = state;
        }

        public Builder recordsProcessed(long r) { this.recordsProcessed = r; return this; }
        public Builder throughputRecordsPerSec(double t) { this.throughputRecordsPerSec = t; return this; }
        public Builder throughputMBPerSec(double t) { this.throughputMBPerSec = t; return this; }
        public Builder avgLatencyMs(double l) { this.avgLatencyMs = l; return this; }
        public Builder p50LatencyMs(double l) { this.p50LatencyMs = l; return this; }
        public Builder p95LatencyMs(double l) { this.p95LatencyMs = l; return this; }
        public Builder p99LatencyMs(double l) { this.p99LatencyMs = l; return this; }
        public Builder p999LatencyMs(double l) { this.p999LatencyMs = l; return this; }
        public Builder maxLatencyMs(double l) { this.maxLatencyMs = l; return this; }
        public Builder error(String e) { this.error = e; return this; }
        public Builder integrityResult(IntegrityResult ir) { this.integrityResult = ir; return this; }
        public Builder timeSeries(List<MetricsSample> ts) { this.timeSeries = ts; return this; }
        public Builder latencyHistogram(Map<String, Double> h) { this.latencyHistogram = h; return this; }
        public Builder heatmapBuckets(long[] b) { this.heatmapBuckets = b; return this; }

        public BenchmarkStatus build() {
            return new BenchmarkStatus(this);
        }
    }
}
