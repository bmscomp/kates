package com.bmscomp.kates.engine;

import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.DoubleAccumulator;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.micrometer.core.instrument.Counter;
import io.micrometer.core.instrument.Gauge;
import io.micrometer.core.instrument.Meter;
import io.micrometer.core.instrument.MeterRegistry;
import io.micrometer.core.instrument.Tags;

/**
 * Bridges internal benchmark metrics to Micrometer for Prometheus export.
 * Each active run registers its own set of labeled meters.
 *
 * <p><b>Cardinality contract.</b> These meters are tagged with {@code run_id},
 * which is unbounded over time, so every meter registered here MUST be removed
 * when the run ends. {@link #endRun(String)} unregisters them; previously it
 * dropped only the map entry and left the meters in the registry forever, so
 * each run permanently added time series and Prometheus memory grew without
 * bound. The number of run_ids alive at once is bounded by the engine's
 * concurrency cap.
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
        // Guard against a double start (retry, replayed event): otherwise
        // activeRuns drifts upward and the previous meters leak.
        RunMeters previous = runMeters.put(runId, new RunMeters(runId, testType, backend));
        if (previous != null) {
            previous.unregister();
        } else {
            activeRuns.incrementAndGet();
        }
    }

    /**
     * Ends a run and UNREGISTERS its meters. Idempotent: the terminal
     * transition, the timeout reaper and the submission-failure path may all
     * reach here for the same run.
     */
    public void endRun(String runId) {
        RunMeters meters = runMeters.remove(runId);
        if (meters == null) {
            return;
        }
        activeRuns.decrementAndGet();
        meters.unregister();
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

    private class RunMeters {
        final String runId;
        final String testType;
        final DoubleAccumulator throughputRecPerSec;
        final DoubleAccumulator throughputMBPerSec;
        private final Map<String, Counter> errorCounters = new ConcurrentHashMap<>();

        /** Every meter this run registered, so endRun can remove all of them. */
        private final List<Meter.Id> meterIds = new CopyOnWriteArrayList<>();

        RunMeters(String runId, String testType, String backend) {
            this.runId = runId;
            this.testType = testType;

            this.throughputRecPerSec = new DoubleAccumulator((a, b) -> b, 0);
            this.throughputMBPerSec = new DoubleAccumulator((a, b) -> b, 0);

            Tags baseTags = Tags.of("run_id", runId, "test_type", testType, "backend", backend);
            meterIds.add(
                    Gauge.builder("kates.benchmark.throughput.rec.sec", throughputRecPerSec, DoubleAccumulator::get)
                            .tags(baseTags)
                            .description("Current throughput in records/sec")
                            .register(registry)
                            .getId());
            meterIds.add(Gauge.builder("kates.benchmark.throughput.mb.sec", throughputMBPerSec, DoubleAccumulator::get)
                    .tags(baseTags)
                    .description("Current throughput in MB/sec")
                    .register(registry)
                    .getId());
        }

        Counter errorCount(String phase) {
            return errorCounters.computeIfAbsent(phase == null ? "default" : phase, p -> {
                Counter counter = Counter.builder("kates.benchmark.errors.total")
                        .tags("run_id", runId, "test_type", testType, "phase", p)
                        .description("Total errors")
                        .register(registry);
                meterIds.add(counter.getId());
                return counter;
            });
        }

        void unregister() {
            for (Meter.Id id : meterIds) {
                registry.remove(id);
            }
            meterIds.clear();
            errorCounters.clear();
        }
    }
}
