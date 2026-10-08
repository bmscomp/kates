package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.*;

import java.time.Instant;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.function.Consumer;
import java.util.stream.Stream;
import jakarta.enterprise.event.Event;
import jakarta.enterprise.inject.Instance;
import jakarta.validation.Validation;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

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
 * The timeout reaper against a run this process is still starting or still
 * running, past its deadline. A PENDING run being submitted holds its
 * concurrency permit but has no handles yet, and its submission can store it
 * RUNNING at any moment. The reaper fails it only with a write conditional on
 * PENDING, and settles it only once that write has landed: settling first
 * would hand back the permit of a run that then goes on producing.
 *
 * <p>Each run here gets a deadline of its creation itself (a max duration and
 * a grace of 0), so it is past it as soon as it is stored.
 */
class TestTimeoutReaperSubmissionTest {

    private static final SpecLimits SPEC_LIMITS =
            new SpecLimits(Validation.buildDefaultValidatorFactory().getValidator());

    private static final int MAX_CONCURRENT = 3;

    /** What the repository holds: the row every read of the run sees. */
    private final Map<String, TestRun> rows = new ConcurrentHashMap<>();

    private final RecordingBackend backend = new RecordingBackend();
    private final BenchmarkMetrics benchmarkMetrics = mock(BenchmarkMetrics.class);

    /** Runs between a conditional write's call and its check, as another writer would. */
    private Runnable beforeConditionalWrite = () -> {};

    private TestOrchestrator orchestrator;
    private TestTimeoutReaper reaper;

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
        when(repository.findByStatus(any()))
                .thenAnswer(invocation -> rows.values().stream()
                        .filter(run -> run.getStatus() == invocation.getArgument(0))
                        .toList());
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
            return true;
        });

        Instance<BenchmarkBackend> backends = mock(Instance.class);
        when(backends.stream()).thenAnswer(invocation -> Stream.of(backend));

        orchestrator = new TestOrchestrator(
                mock(TopicService.class),
                repository,
                backends,
                new TestTypeDefaults(),
                benchmarkMetrics,
                mock(KatesMetrics.class),
                new SlaEvaluator(),
                SPEC_LIMITS,
                mock(Event.class),
                "fake",
                "localhost:9092",
                MAX_CONCURRENT,
                7_200_000L);

        reaper = new TestTimeoutReaper();
        reaper.repository = repository;
        reaper.orchestrator = orchestrator;
        reaper.maxDurationMs = 0;
        reaper.graceMs = 0;
    }

    @Test
    @DisplayName("a PENDING run past its deadline whose tasks are being submitted here is failed, and they stop")
    void aPendingRunBeingSubmittedIsFailedAndItsTasksStop() throws InterruptedException {
        CountDownLatch submitting = new CountDownLatch(1);
        CountDownLatch release = new CountDownLatch(1);
        backend.onSubmit = task -> {
            submitting.countDown();
            awaitRelease(release);
        };
        String id = start();
        assertTrue(submitting.await(5, TimeUnit.SECONDS), "the submission never reached the backend");
        assertEquals(TaskStatus.PENDING, rows.get(id).getStatus());

        reaper.reapStuckTests();

        TestRun reaped = rows.get(id);
        assertEquals(TaskStatus.FAILED, reaped.getStatus());
        assertEquals(List.of(id + "-submission"), taskIds(reaped), "one task carries the reason");
        assertTrue(reaped.getResults().get(0).getError().startsWith("Timeout: still pending "));
        assertEquals(0, orchestrator.activeTestCount(), "the reaper gives back the run's slot");

        release.countDown();

        // The submission finds the run FAILED when it writes, and stops what it started.
        InMemoryEngine.await(() -> backend.stopped.size() == 2, "the tasks of reaped run " + id + " run on");
        assertEquals(backend.submitted, backend.stopped, "every task the submission started is stopped");
        assertSame(reaped, rows.get(id), "the row is the run as the reaper stored it");
        orchestrator.reconcileActiveRuns();
        assertSame(reaped, rows.get(id), "and later ticks leave it so");
        assertEquals(0, orchestrator.activeTestCount(), "no ended run holds a slot");
    }

    @Test
    @DisplayName("a PENDING run its submission stores RUNNING while it is being reaped keeps running, and its slot")
    void aPendingRunThatStartsWhileBeingReapedIsLeftAlone() throws InterruptedException {
        CountDownLatch submitting = new CountDownLatch(1);
        CountDownLatch release = new CountDownLatch(1);
        backend.onSubmit = task -> {
            submitting.countDown();
            awaitRelease(release);
        };
        String id = start();
        assertTrue(submitting.await(5, TimeUnit.SECONDS), "the submission never reached the backend");
        // The submission stores the run RUNNING between the reaper's read and its write.
        AtomicBoolean armed = new AtomicBoolean(true);
        beforeConditionalWrite = () -> {
            if (armed.getAndSet(false)) {
                release.countDown();
                InMemoryEngine.await(
                        () -> rows.get(id).getStatus() == TaskStatus.RUNNING, "run " + id + " never started");
            }
        };

        reaper.reapStuckTests();

        assertFalse(armed.get(), "the submission's write came between the reaper's read and its write");
        TestRun stored = rows.get(id);
        assertEquals(TaskStatus.RUNNING, stored.getStatus(), "the reaper's write does not land");
        assertEquals(List.of(id + "-produce-0", id + "-consume-0"), taskIds(stored));
        assertTrue(backend.stopped.isEmpty(), "no task is stopped");
        assertEquals(1, orchestrator.activeTestCount(), "the run keeps its slot");
        verify(benchmarkMetrics, never()).endRun(id);
    }

    @Test
    @DisplayName("a STOPPING run past its deadline that this process runs is stopped, then failed")
    void aStoppingRunThatRunsHereIsStoppedAndFailed() {
        String id = start();
        InMemoryEngine.await(() -> rows.get(id).getStatus() == TaskStatus.RUNNING, "run " + id + " never started");
        // As a Kates API before 1.25.0 stored a run it was stopping.
        rows.put(id, rows.get(id).withStatus(TaskStatus.STOPPING));

        reaper.reapStuckTests();

        TestRun stored = rows.get(id);
        assertEquals(TaskStatus.FAILED, stored.getStatus());
        assertEquals(backend.submitted, backend.stopped, "every task the run started is stopped");
        for (TestResult r : stored.getResults()) {
            assertEquals(TaskStatus.FAILED, r.getStatus(), r.getTaskId());
            assertTrue(r.getError().startsWith("Timeout: still stopping "), r.getError());
        }
        assertEquals(0, orchestrator.activeTestCount(), "the run gives back its slot");
        verify(benchmarkMetrics).endRun(id);
    }

    /** Starts a run, and waits until its deadline, its creation, has passed. */
    private String start() {
        TestRun run = orchestrator.executeTest(request()).asSuccess().orElseThrow();
        Instant created = Instant.parse(run.getCreatedAt());
        InMemoryEngine.await(() -> Instant.now().isAfter(created), "the clock never passed " + created);
        return run.getId();
    }

    private static List<String> taskIds(TestRun run) {
        return run.getResults().stream().map(TestResult::getTaskId).toList();
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
        TestSpec spec = new TestSpec();
        spec.setTopic("kates.reaper");
        spec.setNumRecords(100_000);
        spec.setNumProducers(1);
        spec.setNumConsumers(1);
        spec.setPartitions(3);
        spec.setReplicationFactor(3);
        spec.setMinInsyncReplicas(2);
        spec.setAcks("all");
        spec.setRecordSize(1024);
        spec.setThroughput(-1);
        spec.setDurationMs(60_000);
        CreateTestRequest request = new CreateTestRequest();
        request.setType(TestType.LOAD);
        request.setSpec(spec);
        return request;
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
