package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;

import io.micrometer.core.instrument.simple.SimpleMeterRegistry;
import io.micrometer.prometheus.PrometheusConfig;
import io.micrometer.prometheus.PrometheusMeterRegistry;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.SlaViolation;

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

    @Test
    @DisplayName("every series the dashboards read is published under the name they read")
    void publishesTheNamesTheDashboardsRead() {
        // The boards read the PUBLISHED spelling, not the registered one, and
        // four of these names were read by two dashboards and the book while
        // being registered nowhere at all. Asserting the scrape output is the
        // only check that would have caught that: Micrometer renames as it
        // publishes (dots to underscores, `_total` onto a counter), so a test
        // against meter ids proves nothing about what Prometheus sees.
        PrometheusMeterRegistry registry = new PrometheusMeterRegistry(PrometheusConfig.DEFAULT);
        BenchmarkMetrics metrics = new BenchmarkMetrics(registry);

        metrics.startRun("run-6", "LOAD", "native");
        metrics.recordThroughput("run-6", "produce", 1000, 12);
        metrics.recordRecords("run-6", "task-a", "produce", 5000);
        metrics.recordLatency("run-6", "task-a", "produce", 1.5, 8, 21, 47, 93);
        metrics.recordError("run-6", "produce");
        metrics.recordSlaViolations("run-6", List.of(SlaViolation.critical("p99LatencyMs", 20, 21)));

        String scrape = registry.scrape();

        assertScraped(scrape, "kates_benchmark_active_runs");
        assertScraped(scrape, "kates_benchmark_throughput_rec_sec{");
        assertScraped(scrape, "kates_benchmark_throughput_mb_sec{");
        assertScraped(scrape, "kates_benchmark_errors_total{");
        // The four that the dashboards and docs/book/09-observability.md read
        // and that nothing registered before this change.
        assertScraped(scrape, "kates_benchmark_records_total{");
        assertScraped(scrape, "kates_benchmark_latency_ms{");
        assertScraped(scrape, "kates_benchmark_latency_ms_max{");
        assertScraped(scrape, "kates_benchmark_sla_violations{");

        // The labels the boards filter and group on, not just the names.
        assertScraped(scrape, "quantile=\"0.999\"");
        assertScraped(scrape, "phase=\"produce\"");
        assertScraped(scrape, "run_id=\"run-6\"");
        assertScraped(scrape, "test_type=\"LOAD\"");
        assertScraped(scrape, "metric=\"p99LatencyMs\"");
        assertScraped(scrape, "severity=\"critical\"");

        // kates_benchmark_records_total must be a COUNTER: the benchmark board
        // runs rate() over it, which is meaningless on a gauge and which
        // Prometheus will happily compute anyway.
        assertScraped(scrape, "# TYPE kates_benchmark_records_total counter");
    }

    @Test
    @DisplayName("a phase's record counter sums its tasks and never goes backwards")
    void recordCounterIsMonotonicAcrossTasks() {
        SimpleMeterRegistry registry = new SimpleMeterRegistry();
        BenchmarkMetrics metrics = new BenchmarkMetrics(registry);

        metrics.startRun("run-7", "LOAD", "native");
        metrics.recordRecords("run-7", "task-a", "produce", 100);
        metrics.recordRecords("run-7", "task-b", "produce", 200);
        assertEquals(300.0, counterValue(registry, "kates.benchmark.records.total", "run-7"), 0.001);

        // A cumulative total that arrives lower than the one before it — a
        // restarted task, a backend that re-counts from zero. Letting it through
        // is a counter reset, and rate() over a reset invents a spike.
        metrics.recordRecords("run-7", "task-a", "produce", 40);
        assertEquals(
                300.0,
                counterValue(registry, "kates.benchmark.records.total", "run-7"),
                0.001,
                "the counter must not fall when a task's cumulative total does");
    }

    @Test
    @DisplayName("a recovered SLA constraint reads zero rather than disappearing")
    void slaViolationsClearToZero() {
        SimpleMeterRegistry registry = new SimpleMeterRegistry();
        BenchmarkMetrics metrics = new BenchmarkMetrics(registry);

        metrics.startRun("run-8", "LOAD", "native");
        metrics.recordSlaViolations("run-8", List.of(SlaViolation.critical("p99LatencyMs", 20, 21)));
        assertEquals(1.0, slaGauge(registry, "run-8", "p99LatencyMs"), 0.001);

        metrics.recordSlaViolations("run-8", List.of());
        assertEquals(
                0.0,
                slaGauge(registry, "run-8", "p99LatencyMs"),
                0.001,
                "a constraint that recovers must read 0; a gap reads the same as never evaluated");
    }

    @Test
    @DisplayName("a phase reports the worst task's percentile, and recovers with it")
    void latencyReportsTheWorstTaskAndRecovers() {
        SimpleMeterRegistry registry = new SimpleMeterRegistry();
        BenchmarkMetrics metrics = new BenchmarkMetrics(registry);

        metrics.startRun("run-9", "LOAD", "native");
        metrics.recordLatency("run-9", "task-a", "produce", 1, 2, 10, 20, 30);
        metrics.recordLatency("run-9", "task-b", "produce", 1, 2, 90, 200, 300);
        assertEquals(90.0, latencyGauge(registry, "run-9", "0.99"), 0.001, "the worst task's p99");

        // Last-writer-wins would leave this at task-b's old 90 forever.
        metrics.recordLatency("run-9", "task-b", "produce", 1, 2, 12, 25, 300);
        assertEquals(
                12.0, latencyGauge(registry, "run-9", "0.99"), 0.001, "the phase recovers when its slowest task does");
    }

    @Test
    @DisplayName("ending a run removes the new meters too")
    void endRunUnregistersTheNewMeters() {
        SimpleMeterRegistry registry = new SimpleMeterRegistry();
        BenchmarkMetrics metrics = new BenchmarkMetrics(registry);

        metrics.startRun("run-10", "LOAD", "native");
        metrics.recordRecords("run-10", "task-a", "produce", 10);
        metrics.recordLatency("run-10", "task-a", "produce", 1, 2, 3, 4, 5);
        metrics.recordSlaViolations("run-10", List.of(SlaViolation.warning("avgLatencyMs", 1, 2)));

        metrics.endRun("run-10");

        assertTrue(
                registry.getMeters().stream()
                        .noneMatch(m -> "run-10".equals(m.getId().getTag("run_id"))),
                "records, latency and sla-violation meters are run-scoped and must go with the run");

        // A poll landing after the run ended must not register a fresh meter:
        // the id list has been cleared, so nothing would ever remove it.
        metrics.recordRecords("run-10", "task-a", "produce", 20);
        metrics.recordLatency("run-10", "task-a", "produce", 1, 2, 3, 4, 5);
        metrics.recordSlaViolations("run-10", List.of(SlaViolation.warning("avgLatencyMs", 1, 2)));
        assertTrue(
                registry.getMeters().stream()
                        .noneMatch(m -> "run-10".equals(m.getId().getTag("run_id"))),
                "a late poll must not resurrect a finished run's series");
    }

    private static void assertScraped(String scrape, String needle) {
        assertTrue(scrape.contains(needle), "the scrape does not contain " + needle + ":\n" + scrape);
    }

    private static double counterValue(SimpleMeterRegistry registry, String name, String runId) {
        var counter = registry.find(name).tag("run_id", runId).functionCounter();
        assertNotNull(counter, name + " is not registered for " + runId);
        return counter.count();
    }

    private static double slaGauge(SimpleMeterRegistry registry, String runId, String metric) {
        var gauge = registry.find("kates.benchmark.sla.violations")
                .tag("run_id", runId)
                .tag("metric", metric)
                .gauge();
        assertNotNull(gauge, "no sla violation gauge for " + metric);
        return gauge.value();
    }

    private static double latencyGauge(SimpleMeterRegistry registry, String runId, String quantile) {
        var gauge = registry.find("kates.benchmark.latency.ms")
                .tag("run_id", runId)
                .tag("quantile", quantile)
                .gauge();
        assertNotNull(gauge, "no latency gauge for quantile " + quantile);
        return gauge.value();
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
