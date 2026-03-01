package com.bmscomp.kates.engine;

import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.DoubleAccumulator;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.micrometer.core.instrument.Counter;
import io.micrometer.core.instrument.DistributionSummary;
import io.micrometer.core.instrument.Gauge;
import io.micrometer.core.instrument.MeterRegistry;
import io.micrometer.core.instrument.Tags;

/**
 * Bridges internal benchmark metrics to Micrometer for Prometheus export.
 * Each active run registers its own set of labeled meters.
 */
@ApplicationScoped
public class BenchmarkMetrics {

    private final MeterRegistry registry;
    private final AtomicInteger activeRuns = new AtomicInteger(0);
    private final Map<String, RunMeters> runMeters = new ConcurrentHashMap<>();

    @Inject
    public BenchmarkMetrics(MeterRegistry registry) {
        this.registry = registry;
        Gauge.builder("kates.benchmark.active.runs", activeRuns, AtomicInteger::get)
                .description("Number of active benchmark runs")
                .register(registry);
    }

    public void startRun(String runId, String testType, String backend) {
        activeRuns.incrementAndGet();
        RunMeters meters = new RunMeters(runId, testType, backend);
        runMeters.put(runId, meters);
    }

    public void endRun(String runId) {
        activeRuns.decrementAndGet();
        runMeters.remove(runId);
    }

    public void recordLatency(String runId, String phaseName, double latencyMs) {
        RunMeters meters = runMeters.get(runId);
        if (meters == null) return;

        meters.latencySummary(phaseName).record(latencyMs);
        meters.recordCount(phaseName).increment();
    }

    public void recordThroughput(String runId, String phaseName, double recPerSec, double mbPerSec) {
        RunMeters meters = runMeters.get(runId);
        if (meters == null) return;

        meters.throughputRecPerSec.accumulate(recPerSec);
        meters.throughputMBPerSec.accumulate(mbPerSec);
    }

    public void recordError(String runId, String phaseName) {
        RunMeters meters = runMeters.get(runId);
        if (meters == null) return;

        meters.errorCount(phaseName).increment();
    }

    public void recordSlaViolation(String runId, String metric, String severity) {
        Counter.builder("kates.benchmark.sla.violations")
                .tags("run_id", runId, "metric", metric, "severity", severity)
                .description("SLA violation events")
                .register(registry)
                .increment();
    }

    private class RunMeters {
        final String runId;
        final String testType;
        final DoubleAccumulator throughputRecPerSec;
        final DoubleAccumulator throughputMBPerSec;
        private final Map<String, DistributionSummary> latencySummaries = new ConcurrentHashMap<>();
        private final Map<String, Counter> recordCounters = new ConcurrentHashMap<>();
        private final Map<String, Counter> errorCounters = new ConcurrentHashMap<>();

        RunMeters(String runId, String testType, String backend) {
            this.runId = runId;
            this.testType = testType;

            this.throughputRecPerSec = new DoubleAccumulator((a, b) -> b, 0);
            this.throughputMBPerSec = new DoubleAccumulator((a, b) -> b, 0);

            Tags baseTags = Tags.of("run_id", runId, "test_type", testType);
            Gauge.builder("kates.benchmark.throughput.rec.sec", throughputRecPerSec, DoubleAccumulator::get)
                    .tags(baseTags)
                    .description("Current throughput in records/sec")
                    .register(registry);
            Gauge.builder("kates.benchmark.throughput.mb.sec", throughputMBPerSec, DoubleAccumulator::get)
                    .tags(baseTags)
                    .description("Current throughput in MB/sec")
                    .register(registry);
        }

        DistributionSummary latencySummary(String phase) {
            return latencySummaries.computeIfAbsent(
                    phase, p -> DistributionSummary.builder("kates.benchmark.latency.ms")
                            .tags("run_id", runId, "test_type", testType, "phase", p)
                            .description("Request latency distribution")
                            .publishPercentiles(0.5, 0.95, 0.99, 0.999)
                            .register(registry));
        }

        Counter recordCount(String phase) {
            return recordCounters.computeIfAbsent(phase, p -> Counter.builder("kates.benchmark.records.total")
                    .tags("run_id", runId, "test_type", testType, "phase", p)
                    .description("Total records processed")
                    .register(registry));
        }

        Counter errorCount(String phase) {
            return errorCounters.computeIfAbsent(phase, p -> Counter.builder("kates.benchmark.errors.total")
                    .tags("run_id", runId, "test_type", testType, "phase", p)
                    .description("Total errors")
                    .register(registry));
        }
    }
}
