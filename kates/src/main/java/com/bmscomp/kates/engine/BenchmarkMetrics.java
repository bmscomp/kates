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

    /**
     * Registers a run's meters, replacing any the same run id already had.
     *
     * <p><b>Unregister-then-register, atomically.</b> Micrometer identifies a
     * meter by name plus tags, and every meter here is tagged with the run id —
     * so an old set and a new set for the same run ARE the same ids. Two
     * consequences, both of which have to be handled in one step:
     *
     * <ul>
     *   <li>Registering first and cleaning up after deletes the meters just
     *       registered, leaving a restarted run exporting nothing at all.
     *   <li>Registering a gauge whose id already exists does NOT rebind it:
     *       Micrometer keeps the first state object and silently drops the new
     *       one. The gauge then reports a {@code DoubleAccumulator} nothing
     *       writes to any more, frozen at its last value.
     * </ul>
     *
     * <p>Doing this as a bare remove followed by a put leaves a window where a
     * concurrent start or end for the same run interleaves and produces exactly
     * that second case. {@code compute} holds the map's per-key lock across the
     * whole transition, so the two can no longer overlap.
     */
    public void startRun(String runId, String testType, String backend) {
        runMeters.compute(runId, (id, previous) -> {
            if (previous != null) {
                // A double start (retry, replayed event). Without this the
                // active-run gauge drifts upward and the old meters leak.
                previous.unregister();
            } else {
                activeRuns.incrementAndGet();
            }
            return new RunMeters(runId, testType, backend);
        });
    }

    /**
     * Ends a run and UNREGISTERS its meters. Idempotent: the terminal
     * transition, the timeout reaper and the submission-failure path may all
     * reach here for the same run.
     *
     * <p>Also under {@code compute}, so it cannot land between a restart's
     * unregister and its re-register and take the new meters with it.
     */
    public void endRun(String runId) {
        runMeters.compute(runId, (id, meters) -> {
            if (meters != null) {
                activeRuns.decrementAndGet();
                meters.unregister();
            }
            return null;
        });
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

        Counter counter = meters.errorCount(phaseName);
        // null once the run's meters have been unregistered — the error still
        // happened, but there is no live series to add it to.
        if (counter != null) {
            counter.increment();
        }
    }

    private class RunMeters {
        final String runId;
        final String testType;
        final DoubleAccumulator throughputRecPerSec;
        final DoubleAccumulator throughputMBPerSec;
        private final Map<String, Counter> errorCounters = new ConcurrentHashMap<>();

        /** Every meter this run registered, so endRun can remove all of them. */
        private final List<Meter.Id> meterIds = new CopyOnWriteArrayList<>();

        /** Set by unregister(); stops a late error from registering a new meter. */
        private volatile boolean unregistered;

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
            // Returns null once the run is over. A late recordError racing
            // unregister() used to register a fresh counter AFTER the id list
            // had been cleared, so that run_id series stayed in the registry
            // for the life of the process — the leak unregister() exists to
            // prevent, reintroduced by the last error of every run that fails
            // at the finish line.
            if (unregistered) {
                return null;
            }
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
            unregistered = true;
            for (Meter.Id id : meterIds) {
                registry.remove(id);
            }
            meterIds.clear();
            errorCounters.clear();
        }
    }
}
