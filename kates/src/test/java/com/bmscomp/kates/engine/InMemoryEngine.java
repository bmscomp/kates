package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.doAnswer;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.function.BooleanSupplier;
import java.util.function.Consumer;
import java.util.stream.Stream;
import jakarta.enterprise.event.Event;
import jakarta.enterprise.inject.Instance;

import com.bmscomp.kates.config.TestTypeDefaults;
import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.TestResult.TaskStatus;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.service.TopicService;

/**
 * A real {@link TestOrchestrator} over an in-memory store and backends whose
 * tasks run until they are stopped, for tests that end a run through an API
 * and then check what it still holds: the tasks its backend was asked to stop
 * and the concurrency permits left.
 *
 * <p>The store keeps the distinction {@link TestRunRepository} draws:
 * {@code save} inserts a run it cannot find, while {@code saveIfPresent} and
 * {@code saveIfStatus} only write over a row that is there.
 */
public final class InMemoryEngine {

    public static final int MAX_CONCURRENT = 3;

    /** The default backend, which {@link #request()} runs on. */
    public static final String BACKEND = "fake";

    /** What the store holds: the row every read of a run sees. */
    public final Map<String, TestRun> rows = new ConcurrentHashMap<>();

    public final RecordingBackend backend = new RecordingBackend(BACKEND);
    public final TestRunRepository repository = mock(TestRunRepository.class);
    public final BenchmarkMetrics benchmarkMetrics = mock(BenchmarkMetrics.class);
    public final KatesMetrics katesMetrics = mock(KatesMetrics.class);

    /** Every lifecycle event the engine fires, in order. */
    public final List<TestLifecycleEvent> events = new CopyOnWriteArrayList<>();

    public final TestOrchestrator orchestrator;

    /** An engine with the default backend and any others a test needs. */
    @SuppressWarnings("unchecked")
    public InMemoryEngine(BenchmarkBackend... others) {
        doAnswer(invocation -> {
                    TestRun run = invocation.getArgument(0);
                    rows.put(run.getId(), run);
                    return null;
                })
                .when(repository)
                .save(any());
        when(repository.saveIfPresent(any())).thenAnswer(invocation -> {
            TestRun run = invocation.getArgument(0);
            return rows.computeIfPresent(run.getId(), (id, stored) -> run) != null;
        });
        when(repository.saveIfStatus(any(), any())).thenAnswer(invocation -> {
            TestRun run = invocation.getArgument(0);
            TestRun stored = rows.get(run.getId());
            if (stored == null || stored.getStatus() != invocation.getArgument(1)) {
                return false;
            }
            rows.put(run.getId(), run);
            return true;
        });
        when(repository.findById(anyString()))
                .thenAnswer(invocation -> Optional.ofNullable(rows.get(invocation.<String>getArgument(0))));
        when(repository.delete(anyString()))
                .thenAnswer(invocation -> Optional.ofNullable(rows.remove(invocation.<String>getArgument(0))));

        List<BenchmarkBackend> all =
                Stream.concat(Stream.of(backend), Stream.of(others)).toList();
        Instance<BenchmarkBackend> backends = mock(Instance.class);
        // A fresh stream per call: a backend is resolved on every poll and stop.
        when(backends.stream()).thenAnswer(invocation -> all.stream());

        Event<TestLifecycleEvent> lifecycleEvents = mock(Event.class);
        when(lifecycleEvents.fireAsync(any())).thenAnswer(invocation -> {
            events.add(invocation.getArgument(0));
            return CompletableFuture.completedFuture(invocation.getArgument(0));
        });

        orchestrator = new TestOrchestrator(
                mock(TopicService.class),
                repository,
                backends,
                new TestTypeDefaults(),
                benchmarkMetrics,
                katesMetrics,
                new SlaEvaluator(),
                lifecycleEvents,
                BACKEND,
                "localhost:9092",
                MAX_CONCURRENT,
                7_200_000L);
    }

    /** The events fired for {@code runId} that announced it ended {@code FAILED}. */
    public List<TestLifecycleEvent> failuresOf(String runId) {
        return events.stream()
                .filter(e -> e.getRunId().equals(runId) && e.getKind() == TestLifecycleEvent.EventKind.FAILED)
                .toList();
    }

    /** Checks the run was announced once as ending FAILED, with the detail "deleted". */
    public void assertAnnouncedDeleted(String runId) {
        List<TestLifecycleEvent> failures = failuresOf(runId);
        assertEquals(1, failures.size(), "one FAILED event for " + runId + ", got " + failures.size());
        assertEquals("deleted", failures.get(0).getDetail());
    }

    /** Starts a LOAD run on the default backend; see {@link #startRunning(RecordingBackend)}. */
    public String startRunning() {
        return startRunning(backend);
    }

    /**
     * Starts a LOAD run on {@code on} as POST /api/tests does, and returns once
     * the run is running as far as ending it goes: its tasks are submitted and
     * the reconciler polls them. Their handles are registered only after the
     * RUNNING row is written, so the row alone says too little.
     */
    public String startRunning(RecordingBackend on) {
        CreateTestRequest request = request();
        request.setBackend(on.name());
        String id = orchestrator.executeTest(request).asSuccess().orElseThrow().getId();
        await(
                () -> {
                    orchestrator.reconcileActiveRuns();
                    return on.polled.containsAll(taskIds(id));
                },
                "run " + id + " never started");
        return id;
    }

    /** Starts as many running runs as the engine takes, and checks it then refuses another. */
    public List<String> fill() {
        List<String> ids = new ArrayList<>();
        for (int i = 0; i < MAX_CONCURRENT; i++) {
            ids.add(startRunning());
        }
        assertFalse(admitsARun(), "the engine is full");
        return ids;
    }

    /** Stores a LOAD run that has already ended with {@code status}, and returns its id. */
    public String storeEnded(TaskStatus status) {
        TestRun run = new TestRun(TestType.LOAD, spec()).withBackend(BACKEND).withStatus(status);
        rows.put(run.getId(), run);
        return run.getId();
    }

    /** Whether the engine takes one more run now; a run it takes starts. */
    public boolean admitsARun() {
        return orchestrator.executeTest(request()).isSuccess();
    }

    /**
     * Every slot is free, and each came back once: max-concurrent-tests new
     * runs start, and the one after them is refused.
     */
    public void assertEverySlotFree() {
        assertEquals(0, orchestrator.activeTestCount(), "no ended run holds a slot");
        for (int i = 1; i <= MAX_CONCURRENT; i++) {
            assertTrue(admitsARun(), "new run " + i + " of " + MAX_CONCURRENT + " starts");
        }
        assertFalse(admitsARun(), "the cap still holds");
    }

    /** The tasks a LOAD run submits: its producer and its consumer. */
    public static List<String> taskIds(String runId) {
        return List.of(runId + "-produce-0", runId + "-consume-0");
    }

    /** Waits up to five seconds for {@code condition}, failing with {@code message}. */
    public static void await(BooleanSupplier condition, String message) {
        long deadline = System.nanoTime() + 5_000_000_000L;
        while (!condition.getAsBoolean()) {
            assertTrue(System.nanoTime() < deadline, message);
            Thread.onSpinWait();
        }
    }

    public static CreateTestRequest request() {
        CreateTestRequest request = new CreateTestRequest();
        request.setType(TestType.LOAD);
        request.setSpec(spec());
        return request;
    }

    private static TestSpec spec() {
        TestSpec spec = new TestSpec();
        spec.setTopic("kates.engine");
        spec.setNumRecords(100_000);
        spec.setNumProducers(1);
        spec.setNumConsumers(1);
        spec.setPartitions(3);
        spec.setReplicationFactor(3);
        spec.setMinInsyncReplicas(2);
        spec.setAcks("all");
        spec.setBatchSize(65536);
        spec.setLingerMs(5);
        spec.setCompressionType("lz4");
        spec.setRecordSize(1024);
        spec.setThroughput(-1);
        spec.setDurationMs(60_000);
        return spec;
    }

    /** A backend whose tasks run until stopped, and which remembers what it was asked. */
    public static final class RecordingBackend implements BenchmarkBackend {

        private final String name;
        public final List<String> submitted = new CopyOnWriteArrayList<>();
        public final List<String> stopped = new CopyOnWriteArrayList<>();
        public final Set<String> polled = ConcurrentHashMap.newKeySet();

        /** Runs inside each poll before it answers, as whatever the poll races would. */
        public volatile Consumer<BenchmarkHandle> onPoll = handle -> {};

        public RecordingBackend(String name) {
            this.name = name;
        }

        @Override
        public String name() {
            return name;
        }

        @Override
        public BenchmarkHandle submit(BenchmarkTask task) {
            submitted.add(task.getTaskId());
            return new BenchmarkHandle(name, task.getTaskId());
        }

        @Override
        public BenchmarkStatus poll(BenchmarkHandle handle) {
            polled.add(handle.taskId());
            onPoll.accept(handle);
            return BenchmarkStatus.builder(stopped.contains(handle.taskId()) ? TaskStatus.FAILED : TaskStatus.RUNNING)
                    .build();
        }

        @Override
        public void stop(BenchmarkHandle handle) {
            if (!stopped.contains(handle.taskId())) {
                stopped.add(handle.taskId());
            }
        }
    }
}
