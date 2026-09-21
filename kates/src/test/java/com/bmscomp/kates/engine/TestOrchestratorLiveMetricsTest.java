package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.*;

import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.ConcurrentHashMap;
import java.util.stream.Stream;
import jakarta.enterprise.event.Event;
import jakarta.enterprise.inject.Instance;

import io.micrometer.prometheus.PrometheusConfig;
import io.micrometer.prometheus.PrometheusMeterRegistry;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.config.TestTypeDefaults;
import com.bmscomp.kates.domain.SlaDefinition;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestResult.TaskStatus;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.service.TopicService;

/**
 * What {@code /q/metrics} publishes WHILE a run is going, driven by the poll
 * the 5s reconciler makes.
 *
 * <p>{@code BenchmarkMetricsTest} proves the meters publish under the names
 * the boards read once something calls them. Nothing proved that anything
 * called them mid-run, and the chain that has to hold is longer than it looks:
 * the orchestrator polls a task only when the handle registered at submission
 * matches the task id persisted on its result, and only a poll feeds the
 * meters. Until the live path existed, the per-run meters were written once,
 * in the instant between the terminal transition and {@code endRun}
 * unregistering them, so {@code kates_benchmark_throughput_rec_sec} was never
 * scraped with a value.
 *
 * <p>Walks the real path — {@code executeAsync} through the native backend's
 * handle shape, then {@code reconcileActiveRuns} — and reads the scrape a
 * Prometheus would have seen between two ticks. Everything a poll answers
 * comes from {@code NativeKafkaBackend.poll} over a real {@code WorkerState},
 * so the numbers are the backend's own arithmetic; only the broker is missing.
 */
class TestOrchestratorLiveMetricsTest {

    private final PrometheusMeterRegistry registry = new PrometheusMeterRegistry(PrometheusConfig.DEFAULT);
    private final BenchmarkMetrics benchmarkMetrics = new BenchmarkMetrics(registry);
    private final ScriptedNativeBackend backend = new ScriptedNativeBackend();

    /** What the repository holds: the row {@code GET /api/tests/{id}} would read. */
    private final Map<String, TestRun> rows = new ConcurrentHashMap<>();

    private TestOrchestrator orchestrator;

    @BeforeEach
    @SuppressWarnings("unchecked")
    void setup() {
        TestRunRepository repository = mock(TestRunRepository.class);
        doAnswer(invocation -> {
                    TestRun run = invocation.getArgument(0);
                    rows.put(run.getId(), run);
                    return null;
                })
                .when(repository)
                .save(any());
        when(repository.findById(anyString()))
                .thenAnswer(invocation -> Optional.ofNullable(rows.get(invocation.<String>getArgument(0))));

        Instance<BenchmarkBackend> backends = mock(Instance.class);
        // A fresh stream per call: resolveBackend runs on every poll.
        when(backends.stream()).thenAnswer(invocation -> Stream.of(backend));

        orchestrator = new TestOrchestrator(
                mock(TopicService.class),
                repository,
                backends,
                new TestTypeDefaults(),
                benchmarkMetrics,
                mock(KatesMetrics.class),
                new SlaEvaluator(),
                mock(Event.class),
                "native",
                "localhost:9092",
                3);
    }

    @Test
    @DisplayName("every meter the benchmark board reads is fed by the reconciler's poll, mid-run")
    void liveMetersFollowThePolls() {
        TestSpec spec = enduranceSpec();
        TestRun run = new TestRun(TestType.ENDURANCE, spec).withBackend("native");
        String id = run.getId();

        orchestrator.executeAsync(run, TestType.ENDURANCE, spec, "native", backend);

        // The persisted results carry the task ids the handles were registered
        // under. This is the join the whole live path hangs on: a result
        // without its task id is a task the reconciler will never poll.
        TestRun submitted = rows.get(id);
        assertEquals(TaskStatus.RUNNING, submitted.getStatus());
        assertEquals(
                List.of(id + "-endurance-produce", id + "-endurance-consume"),
                submitted.getResults().stream().map(TestResult::getTaskId).toList());

        backend.progress(id + "-endurance-produce", 2_000, 5, 8, 21, 47, 93);
        backend.progress(id + "-endurance-consume", 1_990);

        // The scheduler's tick.
        orchestrator.reconcileActiveRuns();

        String scrape = registry.scrape();
        String runId = "run_id=\"" + id + "\"";
        assertEquals(1.0, series(scrape, "kates_benchmark_active_runs"), 0.001);
        assertEquals(
                2_000.0,
                series(scrape, "kates_benchmark_records_total", runId, "test_type=\"ENDURANCE\"", "phase=\"produce\""),
                0.001);
        assertEquals(1_990.0, series(scrape, "kates_benchmark_records_total", runId, "phase=\"consume\""), 0.001);

        double p50 = series(scrape, "kates_benchmark_latency_ms", runId, "phase=\"produce\"", "quantile=\"0.5\"");
        double p99 = series(scrape, "kates_benchmark_latency_ms", runId, "phase=\"produce\"", "quantile=\"0.99\"");
        double max = series(scrape, "kates_benchmark_latency_ms_max", runId, "phase=\"produce\"");
        assertTrue(p50 > 0 && p99 >= p50, "percentiles come from the backend's histogram: p50=" + p50 + " p99=" + p99);
        assertEquals(93.0, max, 1.0, "the worst sample recorded");

        // The gauge carries what the poll answered — records over the worker's
        // ~10s so far — where it used to read 0 for the run's whole life.
        double recPerSec = series(scrape, "kates_benchmark_throughput_rec_sec", runId, "backend=\"native\"");
        assertTrue(recPerSec > 150 && recPerSec < 250, "throughput follows the poll, got " + recPerSec);
        assertTrue(series(scrape, "kates_benchmark_throughput_mb_sec", runId) > 0, "MB/s follows the poll");

        // Registered at zero from the first poll: the board's error-rate panel
        // reads a flat zero for a healthy run rather than No data.
        assertEquals(0.0, series(scrape, "kates_benchmark_errors_total", runId, "phase=\"produce\""), 0.001);
        assertEquals(0.0, series(scrape, "kates_benchmark_errors_total", runId, "phase=\"consume\""), 0.001);

        // The row a client reads between ticks is what this poll wrote: the
        // record counts it sees climbing come from here, task ids intact.
        TestRun polled = rows.get(id);
        assertEquals(TaskStatus.RUNNING, polled.getStatus(), "two running tasks: the run is not over");
        TestResult produce = polled.getResults().get(0);
        assertEquals(id + "-endurance-produce", produce.getTaskId());
        assertEquals(2_000, produce.getRecordsSent());
        assertTrue(produce.getThroughputRecordsPerSec() > 0);

        // Next tick: the counter climbs with the task's cumulative total and
        // the gauge follows the newer poll.
        backend.progress(id + "-endurance-produce", 4_100, 6, 7);
        backend.progress(id + "-endurance-consume", 4_050);
        orchestrator.reconcileActiveRuns();

        scrape = registry.scrape();
        assertEquals(4_100.0, series(scrape, "kates_benchmark_records_total", runId, "phase=\"produce\""), 0.001);
        assertTrue(series(scrape, "kates_benchmark_throughput_rec_sec", runId) > 300, "the gauge moved with the poll");
        assertEquals(4_100, rows.get(id).getResults().get(0).getRecordsSent());
    }

    @Test
    @DisplayName("a task that fails while its sibling runs on is counted by the poll that sees it")
    void failureIsCountedLive() {
        TestSpec spec = enduranceSpec();
        TestRun run = new TestRun(TestType.ENDURANCE, spec).withBackend("native");
        String id = run.getId();
        String runId = "run_id=\"" + id + "\"";

        orchestrator.executeAsync(run, TestType.ENDURANCE, spec, "native", backend);
        backend.progress(id + "-endurance-produce", 500, 5, 6);
        backend.progress(id + "-endurance-consume", 480);
        orchestrator.reconcileActiveRuns();
        assertEquals(0.0, series(registry.scrape(), "kates_benchmark_errors_total", runId, "phase=\"consume\""), 0.001);

        // The consumer dies; the producer has a minute of work left. Counted
        // only at the terminal transition — which unregisters the run's meters
        // in the same call — this failure was never scraped.
        backend.fail(id + "-endurance-consume", "Consumer closed: broker unreachable");
        orchestrator.reconcileActiveRuns();

        String scrape = registry.scrape();
        assertEquals(1.0, series(scrape, "kates_benchmark_errors_total", runId, "phase=\"consume\""), 0.001);
        assertEquals(0.0, series(scrape, "kates_benchmark_errors_total", runId, "phase=\"produce\""), 0.001);
        assertEquals(TaskStatus.RUNNING, rows.get(id).getStatus(), "the producer is still running");
        assertEquals(TaskStatus.FAILED, rows.get(id).getResults().get(1).getStatus());

        // A failed task is not polled again; ticks keep the count where it is.
        orchestrator.reconcileActiveRuns();
        assertEquals(1.0, series(registry.scrape(), "kates_benchmark_errors_total", runId, "phase=\"consume\""), 0.001);

        // The producer finishes: the run settles FAILED and, by the
        // cardinality contract, every run_id series goes with it.
        backend.finish(id + "-endurance-produce");
        orchestrator.reconcileActiveRuns();

        assertEquals(TaskStatus.FAILED, rows.get(id).getStatus());
        scrape = registry.scrape();
        assertFalse(scrape.contains(runId), "no run_id series may outlive the run:\n" + scrape);
        assertEquals(0.0, series(scrape, "kates_benchmark_active_runs"), 0.001);
    }

    @Test
    @DisplayName("an SLA breach is published by the poll that observes it")
    void slaBreachIsPublishedLive() {
        TestSpec spec = enduranceSpec();
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxP99LatencyMs(10.0);
        TestRun run =
                new TestRun(TestType.ENDURANCE, spec).withBackend("native").withSla(sla);
        String id = run.getId();
        String runId = "run_id=\"" + id + "\"";

        orchestrator.executeAsync(run, TestType.ENDURANCE, spec, "native", backend);
        backend.progress(id + "-endurance-produce", 100, 5, 6, 7);
        backend.progress(id + "-endurance-consume", 90);
        orchestrator.reconcileActiveRuns();

        // Within the SLA. A constraint that has never been violated is never
        // registered — the board's zero fallback covers it, by design.
        assertFalse(registry.scrape().contains("kates_benchmark_sla_violations{"), "no violation, no series");

        backend.progress(id + "-endurance-produce", 200, 50, 60, 70);
        orchestrator.reconcileActiveRuns();

        assertEquals(
                1.0,
                series(
                        registry.scrape(),
                        "kates_benchmark_sla_violations",
                        runId,
                        "metric=\"p99LatencyMs\"",
                        "severity=\"critical\""),
                0.001);
    }

    private static TestSpec enduranceSpec() {
        TestSpec spec = new TestSpec();
        spec.setTopic("kates.orders");
        spec.setNumRecords(100_000);
        spec.setRecordSize(512);
        spec.setPartitions(3);
        spec.setReplicationFactor(3);
        spec.setMinInsyncReplicas(2);
        spec.setAcks("all");
        spec.setBatchSize(65536);
        spec.setLingerMs(5);
        spec.setCompressionType("lz4");
        spec.setThroughput(200);
        spec.setDurationMs(70_000);
        return spec;
    }

    /**
     * The value of the one series in a scrape with the given name and every
     * given label, whatever order the exporter wrote the labels in.
     */
    private static double series(String scrape, String name, String... labels) {
        for (String line : scrape.split("\n")) {
            if (!line.startsWith(name + "{") && !line.startsWith(name + " ")) {
                continue;
            }
            boolean matches = true;
            for (String label : labels) {
                if (!line.contains(label)) {
                    matches = false;
                    break;
                }
            }
            if (matches) {
                return Double.parseDouble(line.substring(line.lastIndexOf(' ') + 1));
            }
        }
        throw new AssertionError(
                "no " + name + " series with " + String.join(", ", labels) + " in the scrape:\n" + scrape);
    }

    /**
     * The native backend's handle shape without its broker: {@code submit}
     * builds the same {@code WorkerState} the real one starts a worker over
     * and hands it back inside the handle, and {@code poll} is the real
     * {@code NativeKafkaBackend.poll}, which resolves that state from the
     * handle exactly as it does for a worker its own map has evicted.
     */
    private static final class ScriptedNativeBackend implements BenchmarkBackend {

        private final NativeKafkaBackend delegate = new NativeKafkaBackend("localhost:9092", null, null);
        private final Map<String, NativeKafkaBackend.WorkerState> workers = new ConcurrentHashMap<>();

        @Override
        public String name() {
            return "native";
        }

        @Override
        public BenchmarkHandle submit(BenchmarkTask task) {
            NativeKafkaBackend.WorkerState state = new NativeKafkaBackend.WorkerState(task);
            state.status = TaskStatus.RUNNING;
            // Ten seconds in, so the polled throughput is records over ~10s.
            state.startTimeMs = System.currentTimeMillis() - 10_000;
            workers.put(task.getTaskId(), state);
            return new BenchmarkHandle(name(), task.getTaskId(), state);
        }

        @Override
        public BenchmarkStatus poll(BenchmarkHandle handle) {
            return delegate.poll(handle);
        }

        @Override
        public void stop(BenchmarkHandle handle) {
            delegate.stop(handle);
        }

        void progress(String taskId, long records, double... latenciesMs) {
            NativeKafkaBackend.WorkerState state = worker(taskId);
            state.recordsProcessed.set(records);
            for (double ms : latenciesMs) {
                state.histogram.recordLatency(ms);
            }
        }

        void fail(String taskId, String error) {
            NativeKafkaBackend.WorkerState state = worker(taskId);
            state.status = TaskStatus.FAILED;
            state.error = error;
        }

        void finish(String taskId) {
            worker(taskId).status = TaskStatus.DONE;
        }

        private NativeKafkaBackend.WorkerState worker(String taskId) {
            NativeKafkaBackend.WorkerState state = workers.get(taskId);
            assertNotNull(state, "no worker was submitted for " + taskId);
            return state;
        }
    }
}
