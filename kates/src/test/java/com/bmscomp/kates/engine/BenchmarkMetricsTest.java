package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;

import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;

import io.micrometer.core.instrument.simple.SimpleMeterRegistry;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

/**
 * These meters are tagged with {@code run_id}, which is unbounded over time, so
 * the only thing standing between this class and a registry that grows forever
 * is that every meter a run registers is removed when the run ends.
 */
class BenchmarkMetricsTest {

    @Test
    @DisplayName("ending a run removes its meters")
    void endRunUnregistersMeters() {
        SimpleMeterRegistry registry = new SimpleMeterRegistry();
        BenchmarkMetrics metrics = new BenchmarkMetrics(registry);

        metrics.startRun("run-1", "LOAD", "native");
        metrics.recordThroughput("run-1", "phase", 100, 1);
        metrics.recordError("run-1", "phase");
        assertFalse(registry.getMeters().isEmpty());

        metrics.endRun("run-1");

        assertTrue(
                registry.getMeters().stream()
                        .noneMatch(m -> "run-1".equals(m.getId().getTag("run_id"))),
                "no run_id series may outlive the run");
    }

    @Test
    @DisplayName("an error arriving after the run ended does not resurrect a meter")
    void lateErrorDoesNotReRegister() {
        SimpleMeterRegistry registry = new SimpleMeterRegistry();
        BenchmarkMetrics metrics = new BenchmarkMetrics(registry);

        metrics.startRun("run-2", "LOAD", "native");
        metrics.endRun("run-2");

        // The run is gone from the map, so this is a no-op — the case that
        // matters is a worker still finishing while endRun runs, which the
        // unregistered flag inside RunMeters covers.
        metrics.recordError("run-2", "phase");

        assertTrue(
                registry.getMeters().stream()
                        .noneMatch(m -> "run-2".equals(m.getId().getTag("run_id"))),
                "a late error must not register a fresh counter for a finished run");
    }

    @Test
    @DisplayName("ending a run twice is harmless")
    void endRunIsIdempotent() {
        SimpleMeterRegistry registry = new SimpleMeterRegistry();
        BenchmarkMetrics metrics = new BenchmarkMetrics(registry);

        metrics.startRun("run-3", "STRESS", "native");
        metrics.endRun("run-3");

        // The terminal transition, the reaper and the failure path can all
        // reach here for the same run.
        assertDoesNotThrow(() -> metrics.endRun("run-3"));
    }

    @Test
    @DisplayName("a restarted run still exports, and its gauges follow the NEW state")
    void restartReplacesMeters() {
        SimpleMeterRegistry registry = new SimpleMeterRegistry();
        BenchmarkMetrics metrics = new BenchmarkMetrics(registry);

        metrics.startRun("run-4", "LOAD", "native");
        metrics.recordThroughput("run-4", "phase", 111, 1);

        metrics.startRun("run-4", "LOAD", "native");

        long throughputSeries = registry.getMeters().stream()
                .filter(m -> "run-4".equals(m.getId().getTag("run_id")))
                .filter(m -> m.getId().getName().startsWith("kates.benchmark.throughput"))
                .count();
        assertEquals(2, throughputSeries, "one rec/sec and one MB/sec gauge, not two of each and not none");

        // The value check is the one that matters. Counting series passes even
        // when the surviving gauge is bound to the FIRST run's accumulator,
        // which nothing writes to any more — the gauge would sit frozen at 111
        // while every later measurement went somewhere invisible.
        metrics.recordThroughput("run-4", "phase", 222, 2);

        assertEquals(
                222.0,
                gaugeValue(registry, "kates.benchmark.throughput.rec.sec", "run-4"),
                0.001,
                "the restarted run's gauge must read its own state, not the previous run's");
    }

    @Test
    @DisplayName("an endRun arriving mid-registration cannot leak the meters being registered")
    void endRunCannotInterleaveWithStartRun() throws Exception {
        // The dangerous interleaving is precise, so it is forced rather than
        // raced for: hold a startRun INSIDE its meter registration, then run
        // endRun for the same id.
        //
        // Registering and publishing the map entry as two steps leaves a window
        // where endRun sees no entry, does nothing, and returns — and the
        // start it interrupted then publishes meters for a run that has already
        // ended. Those meters are never unregistered: a permanent run_id series,
        // which is the leak this class exists to avoid. Doing the whole
        // transition under the map's per-key lock makes endRun wait instead.
        CountDownLatch registering = new CountDownLatch(1);
        CountDownLatch release = new CountDownLatch(1);
        BlockingRegistry registry = new BlockingRegistry(registering, release);
        BenchmarkMetrics metrics = new BenchmarkMetrics(registry);

        Thread starter = new Thread(() -> metrics.startRun("run-5", "LOAD", "native"));
        starter.start();

        assertTrue(registering.await(10, TimeUnit.SECONDS), "startRun never reached meter registration");

        Thread ender = new Thread(() -> metrics.endRun("run-5"));
        ender.start();
        // Give the ender long enough to finish if nothing is holding it back.
        ender.join(500);

        release.countDown();
        starter.join(10_000);
        ender.join(10_000);
        assertFalse(starter.isAlive(), "startRun did not finish");
        assertFalse(ender.isAlive(), "endRun did not finish");

        assertTrue(
                registry.getMeters().stream()
                        .noneMatch(m -> "run-5".equals(m.getId().getTag("run_id"))),
                "the run was ended, so none of its meters may still be registered");
    }

    /** A registry that parks the first gauge registration until told to continue. */
    private static final class BlockingRegistry extends SimpleMeterRegistry {
        private final CountDownLatch registering;
        private final CountDownLatch release;
        private final java.util.concurrent.atomic.AtomicBoolean blockedOnce =
                new java.util.concurrent.atomic.AtomicBoolean();

        BlockingRegistry(CountDownLatch registering, CountDownLatch release) {
            this.registering = registering;
            this.release = release;
        }

        @Override
        protected <T> io.micrometer.core.instrument.Gauge newGauge(
                io.micrometer.core.instrument.Meter.Id id,
                T obj,
                java.util.function.ToDoubleFunction<T> valueFunction) {
            // Only a PER-RUN gauge, and only the first one. BenchmarkMetrics
            // registers a global active-runs gauge in its constructor; blocking
            // there would park the constructor before the test can release it.
            if (id.getTag("run_id") != null && blockedOnce.compareAndSet(false, true)) {
                registering.countDown();
                await(release);
            }
            return super.newGauge(id, obj, valueFunction);
        }
    }

    private static double gaugeValue(SimpleMeterRegistry registry, String name, String runId) {
        var gauge = registry.find(name).tag("run_id", runId).gauge();
        assertNotNull(gauge, name + " is not registered for " + runId);
        return gauge.value();
    }

    private static void await(CountDownLatch latch) {
        try {
            latch.await();
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new IllegalStateException(e);
        }
    }
}
