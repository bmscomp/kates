package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;

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
    @DisplayName("restarting the same run id replaces its meters rather than duplicating them")
    void restartReplacesMeters() {
        SimpleMeterRegistry registry = new SimpleMeterRegistry();
        BenchmarkMetrics metrics = new BenchmarkMetrics(registry);

        metrics.startRun("run-4", "LOAD", "native");
        metrics.startRun("run-4", "LOAD", "native");

        long throughputSeries = registry.getMeters().stream()
                .filter(m -> "run-4".equals(m.getId().getTag("run_id")))
                .filter(m -> m.getId().getName().startsWith("kates.benchmark.throughput"))
                .count();

        assertEquals(2, throughputSeries, "one rec/sec and one MB/sec gauge, not two of each");
    }
}
