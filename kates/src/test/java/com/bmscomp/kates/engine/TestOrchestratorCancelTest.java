package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.*;

import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.stream.Stream;
import jakarta.enterprise.event.Event;
import jakarta.enterprise.inject.Instance;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;

import com.bmscomp.kates.config.TestTypeDefaults;
import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestResult.TaskStatus;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.service.TopicService;

/**
 * What a cancel leaves behind. The run is stored FAILED, and nothing settles a
 * FAILED run afterwards: the reconciler's poll returns early for it and the
 * timeout reaper only scans RUNNING. So the cancel itself has to hand back
 * everything the run held, its concurrency slot, its per-run meters and its
 * backend workers, and say that the run ended.
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
        when(repository.saveIfStatus(any(), any())).thenAnswer(invocation -> {
            beforeConditionalWrite.run();
            TestRun run = invocation.getArgument(0);
            TestRun stored = rows.get(run.getId());
            if (stored == null || stored.getStatus() != invocation.getArgument(1)) {
                return false;
            }
            rows.put(run.getId(), run);
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
                MAX_CONCURRENT);
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

    private static CreateTestRequest request() {
        CreateTestRequest request = new CreateTestRequest();
        request.setType(TestType.LOAD);
        request.setSpec(spec());
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

        @Override
        public String name() {
            return "fake";
        }

        @Override
        public BenchmarkHandle submit(BenchmarkTask task) {
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
