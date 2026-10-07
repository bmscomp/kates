package com.bmscomp.kates.resilience;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyLong;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.*;

import java.time.Duration;
import java.time.Instant;
import java.util.Arrays;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.function.LongConsumer;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.ChaosCoordinator;
import com.bmscomp.kates.chaos.ChaosOutcome;
import com.bmscomp.kates.chaos.FaultSpec;
import com.bmscomp.kates.chaos.ProbeExecutor;
import com.bmscomp.kates.chaos.ProbeResult;
import com.bmscomp.kates.chaos.ProbeSpec;
import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.ScenarioPhase;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestResult.TaskStatus;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestScenario;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.engine.BenchmarkBackend;
import com.bmscomp.kates.engine.BenchmarkException;
import com.bmscomp.kates.engine.BenchmarkHandle;
import com.bmscomp.kates.engine.BenchmarkStatus;
import com.bmscomp.kates.engine.BenchmarkTask;
import com.bmscomp.kates.engine.InMemoryEngine;
import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.report.ReportGenerator;
import com.bmscomp.kates.report.TestReport;
import com.bmscomp.kates.util.Result;

/**
 * The fault goes only into a benchmark that is running. One that had ended,
 * or had not started, got it all the same: the fault hit a cluster with no
 * load on it, and the report, its summaries taken from a run the fault never
 * touched, said COMPLETED.
 */
class ResilienceOrchestratorBenchmarkStatusTest {

    private final ResilienceOrchestrator orchestrator = new ResilienceOrchestrator();

    /** A run as executeTest creates it: PENDING, with no tasks yet. */
    private final TestRun created = new TestRun(TestType.LOAD, new TestSpec());

    private final String id = created.getId();

    @BeforeEach
    void wire() {
        orchestrator.testOrchestrator = mock(TestOrchestrator.class);

        Instant now = Instant.now();
        orchestrator.chaosCoordinator = mock(ChaosCoordinator.class);
        // A provider reports the moment the fault goes in, and the run marks it.
        when(orchestrator.chaosCoordinator.triggerFault(any(), any())).thenAnswer(invocation -> {
            invocation.<LongConsumer>getArgument(1).accept(System.nanoTime());
            return CompletableFuture.completedFuture(
                    ChaosOutcome.success("engine", "pod-kill", now, now, System.nanoTime(), null, null, null));
        });

        orchestrator.reportGenerator = mock(ReportGenerator.class);
        when(orchestrator.reportGenerator.generate(any())).thenReturn(new TestReport());

        // A passing Edge probe keeps the run off the fixed 10s no-probe wait.
        orchestrator.probeExecutor = mock(ProbeExecutor.class);
        when(orchestrator.probeExecutor.evaluateAll(any(), any())).thenReturn(List.of(ProbeResult.pass("up", "", 1)));

        orchestrator.submissionPollIntervalMs = 1;
    }

    /** executeTest answers with {@code answered}, and each later read of the run is the next of {@code reads}. */
    private void benchmark(TestRun answered, TestRun... reads) {
        when(orchestrator.testOrchestrator.executeTest(any())).thenReturn(Result.success(answered));
        if (reads.length > 0) {
            when(orchestrator.testOrchestrator.refreshStatus(id))
                    .thenReturn(reads[0], Arrays.copyOfRange(reads, 1, reads.length));
        }
    }

    private ResilienceReport execute(CreateTestRequest testRequest, int steadyStateSec) {
        ResilienceTestRequest request = new ResilienceTestRequest();
        request.setTestRequest(testRequest);
        request.setSteadyStateSec(steadyStateSec);
        request.setMaxRecoveryWaitSec(5);
        request.setProbes(List.of(ProbeSpec.builder("up").build()));
        request.setChaosSpec(FaultSpec.builder("pod-kill")
                .targetNamespace("kafka")
                .chaosDurationSec(1)
                .build());
        return orchestrator.execute(request);
    }

    private ResilienceReport execute(int steadyStateSec) {
        return execute(new CreateTestRequest(), steadyStateSec);
    }

    /** The run in {@code status}, with its producer and consumer in {@code status} too. */
    private TestRun loadRun(TaskStatus status) {
        return created.withStatus(status)
                .withResults(List.of(task("-produce-0", status, null), task("-consume-0", status, null)));
    }

    private TestResult task(String suffix, TaskStatus status, String error) {
        return new TestResult().withTaskId(id + suffix).withStatus(status).withError(error);
    }

    /** No fault went in, none was marked on the run, and no probe ran. */
    private void assertNoFault() {
        verify(orchestrator.chaosCoordinator, never()).triggerFault(any(), any());
        verify(orchestrator.testOrchestrator, never()).markChaosStart(anyString(), anyLong());
        verifyNoInteractions(orchestrator.probeExecutor);
    }

    @Test
    void aRunningRunGetsItsFault() {
        benchmark(loadRun(TaskStatus.RUNNING), loadRun(TaskStatus.RUNNING));

        ResilienceReport report = execute(0);

        assertEquals("COMPLETED", report.getStatus());
        assertNull(report.getError());
        verify(orchestrator.testOrchestrator).markChaosStart(eq(id), anyLong());
        verify(orchestrator.chaosCoordinator).triggerFault(any(), any());
    }

    /**
     * A scenario's tasks are submitted before executeTest answers, so its run
     * can come back FAILED: here a later phase failed to build, and the
     * submission stopped the phase before it. The test used to wait out the
     * steady state and inject the fault all the same.
     */
    @Test
    void aRunThatFailedAtSubmissionGetsNoFault() {
        String stopped = "Stopped: the run failed while its tasks were being submitted:"
                + " java.lang.IllegalStateException: phase peak could not be built";
        benchmark(created.withStatus(TaskStatus.FAILED)
                .withResults(List.of(task("-warmup-produce", TaskStatus.FAILED, stopped))));

        // Answered at once: an hour of steady state is not waited out first.
        ResilienceReport report = assertTimeoutPreemptively(Duration.ofSeconds(10), () -> execute(3600));

        assertEquals("ERROR", report.getStatus());
        assertEquals(
                "The benchmark ended before the fault: run " + id + " is FAILED, so no fault was injected."
                        + " Task errors: " + id + "-warmup-produce: " + stopped,
                report.getError());
        assertNoFault();
        verify(orchestrator.testOrchestrator, never()).refreshStatus(anyString());
    }

    @Test
    void aRunThatFailsDuringTheSteadyStateGetsNoFault() {
        String timedOut = "org.apache.kafka.common.errors.TimeoutException: Topic kates.engine not present in"
                + " metadata after 60000 ms.";
        benchmark(
                loadRun(TaskStatus.RUNNING),
                created.withStatus(TaskStatus.FAILED)
                        .withResults(List.of(
                                task("-produce-0", TaskStatus.FAILED, timedOut),
                                task("-consume-0", TaskStatus.DONE, null))));

        ResilienceReport report = execute(0);

        assertEquals("ERROR", report.getStatus());
        assertEquals(
                "The benchmark ended before the fault: run " + id + " is FAILED, so no fault was injected."
                        + " Task errors: " + id + "-produce-0: " + timedOut,
                report.getError());
        assertNull(report.getPreChaosSummary(), "no snapshot of a run the fault never reached");
        assertNoFault();
    }

    /** An unthrottled run can finish within the steady state. */
    @Test
    void aRunThatFinishedBeforeTheFaultGetsNoFault() {
        benchmark(loadRun(TaskStatus.RUNNING), loadRun(TaskStatus.DONE));

        ResilienceReport report = execute(0);

        assertEquals("ERROR", report.getStatus());
        assertEquals(
                "The benchmark ended before the fault: run " + id + " is DONE, so no fault was injected",
                report.getError());
        assertNoFault();
    }

    /**
     * executeTest answers a plain test's run PENDING and submits its tasks on
     * a thread of its own, so the run is read again until they are submitted,
     * and the steady state counts from then.
     */
    @Test
    void aRunWhoseTasksAreStillBeingSubmittedIsWaitedFor() {
        benchmark(created, created, created, loadRun(TaskStatus.RUNNING));

        ResilienceReport report = execute(0);

        assertEquals("COMPLETED", report.getStatus());
        verify(orchestrator.chaosCoordinator).triggerFault(any(), any());
    }

    /**
     * With a steadyStateSec of 0, the run was read once, still PENDING, and
     * the fault went in before its tasks had failed to submit.
     */
    @Test
    void aRunWhoseTasksFailToSubmitGetsNoFault() {
        String refused = "Trogdor submission failed: Connection refused";
        benchmark(
                created,
                created,
                created.withStatus(TaskStatus.FAILED)
                        .withResults(List.of(
                                task("-produce-0", TaskStatus.FAILED, refused),
                                task("-consume-0", TaskStatus.FAILED, refused))));

        ResilienceReport report = execute(0);

        assertEquals("ERROR", report.getStatus());
        assertEquals(
                "The benchmark ended before the fault: run " + id + " is FAILED, so no fault was injected."
                        + " Task errors: " + id + "-produce-0: " + refused + "; " + id + "-consume-0: " + refused,
                report.getError());
        assertNoFault();
    }

    @Test
    void aRunStillPendingWhenTheWaitRunsOutGetsNoFault() {
        orchestrator.submissionWaitMs = 1_000;
        orchestrator.submissionPollIntervalMs = 50;
        benchmark(created, created);

        ResilienceReport report = execute(0);

        assertEquals("ERROR", report.getStatus());
        assertEquals(
                "The benchmark had not started 1 s after it was created: run " + id
                        + " is still PENDING, so no fault was injected",
                report.getError());
        assertNoFault();
    }

    /** Through a real TestOrchestrator, whose backend fails every task it is given. */
    @Nested
    class ThroughTheEngine {

        private final FailingBackend failing = new FailingBackend();
        private final InMemoryEngine engine = new InMemoryEngine(failing);

        @BeforeEach
        void useTheEngine() {
            orchestrator.testOrchestrator = engine.orchestrator;
        }

        private String onlyRun() {
            assertEquals(1, engine.rows.size());
            return engine.rows.keySet().iterator().next();
        }

        /**
         * The engine is real, so the fault is checked where it would go in:
         * the chaos coordinator was asked nothing.
         */
        private void assertNoFaultReachedTheCoordinator() {
            verify(orchestrator.chaosCoordinator, never()).triggerFault(any(), any());
            verifyNoInteractions(orchestrator.chaosCoordinator, orchestrator.probeExecutor);
        }

        @Test
        void aPlainTestWhoseTasksFailToSubmitGetsNoFault() {
            CreateTestRequest request = InMemoryEngine.request();
            request.setBackend(failing.name());

            ResilienceReport report = execute(request, 0);

            String run = onlyRun();
            assertEquals(TaskStatus.FAILED, engine.rows.get(run).getStatus());
            assertEquals("ERROR", report.getStatus());
            assertEquals(
                    "The benchmark ended before the fault: run " + run + " is FAILED, so no fault was injected."
                            + " Task errors: " + run + "-produce-0: " + FailingBackend.ERROR + "; " + run
                            + "-consume-0: " + FailingBackend.ERROR,
                    report.getError());
            assertNoFaultReachedTheCoordinator();
        }

        @Test
        void aScenarioWhoseTasksFailToSubmitGetsNoFault() {
            TestScenario scenario = new TestScenario();
            scenario.setName("warm-then-peak");
            scenario.setType(TestType.LOAD);
            scenario.setBaseSpec(new TestSpec());
            scenario.setPhases(List.of(
                    new ScenarioPhase("warmup", ScenarioPhase.PhaseType.WARMUP, 0, -1),
                    new ScenarioPhase("peak", ScenarioPhase.PhaseType.STEADY, 0, -1)));
            CreateTestRequest request = new CreateTestRequest();
            request.setType(TestType.LOAD);
            request.setBackend(failing.name());
            request.setScenario(scenario);

            ResilienceReport report = execute(request, 0);

            String run = onlyRun();
            assertEquals("ERROR", report.getStatus());
            assertEquals(
                    "The benchmark ended before the fault: run " + run + " is FAILED, so no fault was injected."
                            + " Task errors: " + run + "-warmup-produce: " + FailingBackend.ERROR + "; " + run
                            + "-peak-produce: " + FailingBackend.ERROR,
                    report.getError());
            assertNoFaultReachedTheCoordinator();
        }
    }

    /**
     * A backend that fails each task a moment after it is given it, as one
     * whose workers cannot be reached does; the moment keeps a plain test's
     * run PENDING past a steady state of 0.
     */
    private static final class FailingBackend implements BenchmarkBackend {

        static final String ERROR = "no worker took the task";

        @Override
        public String name() {
            return "failing";
        }

        @Override
        public BenchmarkHandle submit(BenchmarkTask task) {
            try {
                Thread.sleep(100);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }
            throw new BenchmarkException(ERROR);
        }

        @Override
        public BenchmarkStatus poll(BenchmarkHandle handle) {
            throw new AssertionError("a task that failed to submit is never polled");
        }

        @Override
        public void stop(BenchmarkHandle handle) {}
    }
}
