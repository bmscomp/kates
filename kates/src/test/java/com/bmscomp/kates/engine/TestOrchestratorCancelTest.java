package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.*;

import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.Consumer;
import java.util.stream.Stream;
import jakarta.enterprise.event.Event;
import jakarta.enterprise.inject.Instance;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;

import com.bmscomp.kates.config.TestTypeDefaults;
import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.ScenarioPhase;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestResult.TaskStatus;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestScenario;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.service.TopicService;

/**
 * What a cancel leaves behind. The run is stored FAILED, and nothing polls or
 * reaps a FAILED run afterwards: the reconciler's poll returns early for it and
 * the timeout reaper only scans RUNNING. So the cancel itself has to hand back
 * everything the run held, its concurrency slot, its per-run meters and its
 * backend workers, and say that the run ended. A cancel that lands while the
 * run's tasks are being submitted finds no workers to stop yet, and the
 * submission must not write over it.
 */
class TestOrchestratorCancelTest {

    private static final int MAX_CONCURRENT = 3;

    /** What the repository holds: the row every read of the run sees. */
    private final Map<String, TestRun> rows = new ConcurrentHashMap<>();

    private final RecordingBackend backend = new RecordingBackend();
    private final BenchmarkMetrics benchmarkMetrics = mock(BenchmarkMetrics.class);
    private final KatesMetrics katesMetrics = mock(KatesMetrics.class);

    /** Runs between the cancel's read and its write, as another writer would. */
    private Runnable beforeConditionalWrite = () -> {};

    /** Runs once a conditional write has landed, with the run it stored. */
    private Consumer<TestRun> afterConditionalWrite = run -> {};

    private Event<TestLifecycleEvent> events;
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
        when(repository.saveIfPresent(any())).thenAnswer(invocation -> {
            TestRun run = invocation.getArgument(0);
            return rows.computeIfPresent(run.getId(), (id, stored) -> run) != null;
        });
        when(repository.saveIfStatus(any(), any())).thenAnswer(invocation -> {
            beforeConditionalWrite.run();
            TestRun run = invocation.getArgument(0);
            TestRun stored = rows.get(run.getId());
            if (stored == null || stored.getStatus() != invocation.getArgument(1)) {
                return false;
            }
            rows.put(run.getId(), run);
            afterConditionalWrite.accept(run);
            return true;
        });

        Instance<BenchmarkBackend> backends = mock(Instance.class);
        when(backends.stream()).thenAnswer(invocation -> Stream.of(backend));
        events = mock(Event.class);

        orchestrator = new TestOrchestrator(
                mock(TopicService.class),
                repository,
                backends,
                new TestTypeDefaults(),
                benchmarkMetrics,
                katesMetrics,
                new SlaEvaluator(),
                events,
                "fake",
                "localhost:9092",
                MAX_CONCURRENT,
                7_200_000L);
    }

    @Test
    @DisplayName("a cancelled run gives back its concurrency slot, so cancels cannot block new runs")
    void cancelFreesTheSlot() {
        for (int i = 0; i < MAX_CONCURRENT; i++) {
            String id = startAndAwaitRunning();
            orchestrator.cancelTest(id).orElseThrow();
            // The tick after the cancel finds the run FAILED and settles
            // nothing, so the slot must already be free by now.
            orchestrator.reconcileActiveRuns();
        }

        assertEquals(0, orchestrator.activeTestCount(), "no cancelled run holds a slot");
        assertTrue(
                orchestrator.executeTest(request()).isSuccess(), "a new run starts after max-concurrent-tests cancels");
    }

    @Test
    @DisplayName("a cancel stops the workers, ends the run's meters and says the run ended")
    void cancelEndsTheRun() {
        TestSpec spec = spec();
        TestRun run = new TestRun(TestType.LOAD, spec).withBackend("fake");
        String id = run.getId();
        // Stored first, as executeTest stores it before it submits.
        rows.put(id, run);
        orchestrator.executeAsync(run, TestType.LOAD, spec, "fake", backend);
        assertEquals(TaskStatus.RUNNING, rows.get(id).getStatus());

        TestRun cancelled = orchestrator.cancelTest(id).orElseThrow();

        assertEquals(TaskStatus.FAILED, cancelled.getStatus());
        assertEquals(TaskStatus.FAILED, rows.get(id).getStatus(), "the answer is the run as stored");
        for (TestResult r : rows.get(id).getResults()) {
            assertEquals(TaskStatus.FAILED, r.getStatus());
            assertEquals("Cancelled by user", r.getError());
        }
        assertEquals(backend.submitted, backend.stopped, "every worker the run started is stopped");
        verify(benchmarkMetrics).endRun(id);
        verify(katesMetrics).recordTestCompleted("LOAD", "failed");

        @SuppressWarnings("unchecked")
        ArgumentCaptor<TestLifecycleEvent> fired = ArgumentCaptor.forClass(TestLifecycleEvent.class);
        verify(events, atLeastOnce()).fireAsync(fired.capture());
        TestLifecycleEvent last = fired.getAllValues().get(fired.getAllValues().size() - 1);
        assertEquals(id, last.getRunId());
        assertEquals(TestLifecycleEvent.EventKind.FAILED, last.getKind(), "a stream subscriber sees the run end");
        assertEquals("cancelled", last.getDetail());

        // Later ticks leave the cancelled run as it was stored.
        orchestrator.reconcileActiveRuns();
        assertEquals(TaskStatus.FAILED, rows.get(id).getStatus());
    }

    @Test
    @DisplayName("a run that ends while it is being cancelled keeps the ending it had")
    void aRunThatEndsFirstIsLeftAlone() {
        TestRun run = new TestRun(TestType.LOAD, spec())
                .withBackend("fake")
                .withStatus(TaskStatus.RUNNING)
                .withResults(List.of(new TestResult().withTaskId("t").withStatus(TaskStatus.RUNNING)));
        rows.put(run.getId(), run);
        // The reconciler settles the run between the cancel's read and its write.
        beforeConditionalWrite = () -> rows.put(run.getId(), run.withStatus(TaskStatus.DONE));

        RunNotCancellableException e =
                assertThrows(RunNotCancellableException.class, () -> orchestrator.cancelTest(run.getId()));

        assertEquals(TaskStatus.DONE, e.getStatus());
        assertEquals(TaskStatus.DONE, rows.get(run.getId()).getStatus(), "the real ending is not overwritten");
    }

    @Test
    @DisplayName("a run that has ended cannot be cancelled, and an unknown one is not found")
    void onlyALiveRunIsCancellable() {
        TestRun done = new TestRun(TestType.LOAD, spec()).withBackend("fake").withStatus(TaskStatus.DONE);
        rows.put(done.getId(), done);

        RunNotCancellableException e =
                assertThrows(RunNotCancellableException.class, () -> orchestrator.cancelTest(done.getId()));
        assertEquals(TaskStatus.DONE, e.getStatus());
        assertEquals("Test is not running (status: DONE)", e.getMessage());
        assertTrue(orchestrator.cancelTest("no-such-run").isEmpty());
    }

    @Test
    @DisplayName("a cancel that lands while a run's tasks are being submitted stays a cancel, and stops them")
    void aCancelDuringSubmissionSticks() throws InterruptedException {
        // executeTest answers with the run, PENDING, before its tasks are
        // submitted, so a client can cancel it by id while they are. The
        // submission's write then landed on the cancel's, RUNNING over FAILED,
        // and the run produced without the slot the cancel had given back.
        CountDownLatch submitting = new CountDownLatch(1);
        CountDownLatch release = new CountDownLatch(1);
        backend.onSubmit = task -> {
            submitting.countDown();
            awaitRelease(release);
        };
        String id =
                orchestrator.executeTest(request()).asSuccess().orElseThrow().getId();
        assertTrue(submitting.await(5, TimeUnit.SECONDS), "the submission never reached the backend");

        TestRun cancelled = orchestrator.cancelTest(id).orElseThrow();
        release.countDown();

        // The submission finds the run cancelled when it writes, and stops what it started.
        InMemoryEngine.await(() -> backend.stopped.size() == 2, "the tasks of cancelled run " + id + " run on");
        assertEquals(backend.submitted, backend.stopped, "every task the submission started is stopped");
        assertEquals(TaskStatus.FAILED, cancelled.getStatus());
        assertSame(cancelled, rows.get(id), "the row is the run as the cancel stored it");
        orchestrator.reconcileActiveRuns();
        assertSame(cancelled, rows.get(id), "and later ticks leave it so");
        assertEquals(List.of("cancelled"), endingsOf(id), "the run's end is announced once, by the cancel");
        verify(katesMetrics).recordTestCompleted("LOAD", "failed");
        verify(benchmarkMetrics, atLeastOnce()).endRun(id);
        assertEverySlotFree();
    }

    @Test
    @DisplayName("a cancel that lands as the submission writes still stops the run's tasks")
    void aCancelRightAfterTheSubmissionsWriteStopsItsTasks() {
        // The cancel reads the row the submission has just written, RUNNING
        // with its tasks, and settles the run before the submission publishes
        // their handles, so it finds none to stop. The handles come after the
        // run ended, and the reconciler dropped such a run's handles without
        // stopping them.
        AtomicBoolean armed = new AtomicBoolean(true);
        afterConditionalWrite = stored -> {
            if (stored.getStatus() == TaskStatus.RUNNING && armed.getAndSet(false)) {
                orchestrator.cancelTest(stored.getId()).orElseThrow();
            }
        };

        String id =
                orchestrator.executeTest(request()).asSuccess().orElseThrow().getId();

        InMemoryEngine.await(
                () -> {
                    orchestrator.reconcileActiveRuns();
                    return backend.stopped.size() == 2;
                },
                "the tasks of cancelled run " + id + " run on");
        assertFalse(armed.get(), "the cancel came right after the submission's write");
        assertEquals(backend.submitted, backend.stopped, "every task the submission started is stopped");
        TestRun stored = rows.get(id);
        assertEquals(TaskStatus.FAILED, stored.getStatus());
        assertEquals(2, stored.getResults().size(), "the cancel read the run with its tasks");
        for (TestResult r : stored.getResults()) {
            assertEquals(TaskStatus.FAILED, r.getStatus());
            assertEquals("Cancelled by user", r.getError());
        }
        assertEquals(List.of("cancelled"), endingsOf(id));
        assertEverySlotFree();
    }

    @Test
    @DisplayName("a scenario cancelled while its phases are being submitted stays cancelled, and its tasks stop")
    void aScenarioCancelledDuringSubmissionStaysCancelled() throws Exception {
        // A scenario's tasks are submitted before executeTest answers, but its
        // run is stored, RUNNING, before they are, so a client that lists the
        // runs can cancel it by id in between.
        CountDownLatch submitting = new CountDownLatch(1);
        CountDownLatch release = new CountDownLatch(1);
        AtomicReference<String> runId = new AtomicReference<>();
        backend.onSubmit = task -> {
            runId.compareAndSet(null, task.getRunId());
            submitting.countDown();
            awaitRelease(release);
        };
        CompletableFuture<TestRun> answer = CompletableFuture.supplyAsync(
                () -> orchestrator.executeTest(scenarioRequest()).asSuccess().orElseThrow());
        assertTrue(submitting.await(5, TimeUnit.SECONDS), "the submission never reached the backend");
        String id = runId.get();

        TestRun cancelled = orchestrator.cancelTest(id).orElseThrow();
        release.countDown();

        assertSame(cancelled, answer.get(5, TimeUnit.SECONDS), "executeTest answers with the run as cancelled");
        assertSame(cancelled, rows.get(id), "the row is the run as the cancel stored it");
        assertEquals(List.of(id + "-warmup-produce", id + "-steady-produce"), backend.submitted);
        assertEquals(backend.submitted, backend.stopped, "every task the submission started is stopped");
        assertEquals(List.of("cancelled"), endingsOf(id));
        assertEverySlotFree();
    }

    private String startAndAwaitRunning() {
        TestRun run = orchestrator.executeTest(request()).asSuccess().orElseThrow();
        // Submission runs on a virtual thread; wait for the row it writes.
        long deadline = System.nanoTime() + 5_000_000_000L;
        while (rows.get(run.getId()).getStatus() != TaskStatus.RUNNING) {
            assertTrue(System.nanoTime() < deadline, "run " + run.getId() + " never started");
            Thread.onSpinWait();
        }
        return run.getId();
    }

    /**
     * Every slot is free, and each came back once: max-concurrent-tests new
     * runs start, and the one after them is refused.
     */
    private void assertEverySlotFree() {
        assertEquals(0, orchestrator.activeTestCount(), "no ended run holds a slot");
        for (int i = 1; i <= MAX_CONCURRENT; i++) {
            assertTrue(
                    orchestrator.executeTest(request()).isSuccess(),
                    "new run " + i + " of " + MAX_CONCURRENT + " starts");
        }
        assertFalse(orchestrator.executeTest(request()).isSuccess(), "the cap still holds");
    }

    /** The detail of each event that announced the run ended, in the order they were fired. */
    private List<String> endingsOf(String runId) {
        @SuppressWarnings("unchecked")
        ArgumentCaptor<TestLifecycleEvent> fired = ArgumentCaptor.forClass(TestLifecycleEvent.class);
        verify(events, atLeastOnce()).fireAsync(fired.capture());
        return fired.getAllValues().stream()
                .filter(e -> e.getRunId().equals(runId))
                .filter(e -> e.getKind() == TestLifecycleEvent.EventKind.FAILED
                        || e.getKind() == TestLifecycleEvent.EventKind.DONE)
                .map(TestLifecycleEvent::getDetail)
                .toList();
    }

    /** Holds a backend's submit until the test releases it. */
    private static void awaitRelease(CountDownLatch release) {
        try {
            assertTrue(release.await(5, TimeUnit.SECONDS), "the held submit was never released");
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new IllegalStateException(e);
        }
    }

    private static CreateTestRequest request() {
        CreateTestRequest request = new CreateTestRequest();
        request.setType(TestType.LOAD);
        request.setSpec(spec());
        return request;
    }

    /** A LOAD scenario of two phases, one producer each, on the spec the plain runs use. */
    private static CreateTestRequest scenarioRequest() {
        TestScenario scenario = new TestScenario();
        scenario.setName("cancelled-while-submitted");
        scenario.setType(TestType.LOAD);
        scenario.setBaseSpec(spec());
        scenario.setPhases(List.of(
                new ScenarioPhase("warmup", ScenarioPhase.PhaseType.WARMUP, 0, -1),
                new ScenarioPhase("steady", ScenarioPhase.PhaseType.STEADY, 0, -1)));
        CreateTestRequest request = new CreateTestRequest();
        request.setType(TestType.LOAD);
        request.setScenario(scenario);
        return request;
    }

    private static TestSpec spec() {
        TestSpec spec = new TestSpec();
        spec.setTopic("kates.cancel");
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

    /** A backend whose tasks run until stopped, and which remembers what it stopped. */
    private static final class RecordingBackend implements BenchmarkBackend {

        final List<String> submitted = new CopyOnWriteArrayList<>();
        final List<String> stopped = new CopyOnWriteArrayList<>();

        /** Runs inside each submit before the task starts, as a slow backend's would. */
        volatile Consumer<BenchmarkTask> onSubmit = task -> {};

        @Override
        public String name() {
            return "fake";
        }

        @Override
        public BenchmarkHandle submit(BenchmarkTask task) {
            onSubmit.accept(task);
            submitted.add(task.getTaskId());
            return new BenchmarkHandle(name(), task.getTaskId());
        }

        @Override
        public BenchmarkStatus poll(BenchmarkHandle handle) {
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
