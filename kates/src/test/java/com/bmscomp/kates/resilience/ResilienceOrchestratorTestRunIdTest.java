package com.bmscomp.kates.resilience;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

import java.time.Instant;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeoutException;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.Answers;

import com.bmscomp.kates.chaos.ChaosCoordinator;
import com.bmscomp.kates.chaos.ChaosOutcome;
import com.bmscomp.kates.chaos.FaultSpec;
import com.bmscomp.kates.chaos.ProbeExecutor;
import com.bmscomp.kates.chaos.ProbeResult;
import com.bmscomp.kates.chaos.ProbeSpec;
import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.TestResult.TaskStatus;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.report.ReportGenerator;
import com.bmscomp.kates.report.TestReport;
import com.bmscomp.kates.util.Result;

/**
 * Every report of a run whose test run started names that run, however the
 * report ends. The run used to be named only in the performance report,
 * which comes after the recovery wait, so a report that ended ERROR or
 * INTERRUPTED before it left the run producing under an id nobody had seen.
 */
class ResilienceOrchestratorTestRunIdTest {

    private final ResilienceOrchestrator orchestrator = new ResilienceOrchestrator();

    private final TestRun created = new TestRun(TestType.INTEGRITY, new TestSpec());

    private final String id = created.getId();

    /** What the fault's future answers. */
    private CompletableFuture<ChaosOutcome> fault;

    @BeforeEach
    void wire() {
        orchestrator.testOrchestrator = mock(TestOrchestrator.class);

        Instant now = Instant.now();
        fault = CompletableFuture.completedFuture(
                ChaosOutcome.success("engine", "pod-kill", now, now, System.nanoTime(), null, null, null));
        // Each call that answers a future gets the fault's: these tests are
        // about the report, not about how the fault is triggered.
        orchestrator.chaosCoordinator = mock(
                ChaosCoordinator.class,
                call -> call.getMethod().getReturnType() == CompletableFuture.class
                        ? fault
                        : Answers.RETURNS_DEFAULTS.answer(call));

        orchestrator.reportGenerator = mock(ReportGenerator.class);
        when(orchestrator.reportGenerator.generate(any())).thenReturn(new TestReport());

        orchestrator.probeExecutor = mock(ProbeExecutor.class);
        when(orchestrator.probeExecutor.evaluateAll(any(), any())).thenReturn(List.of(ProbeResult.pass("up", "", 1)));

        orchestrator.submissionPollIntervalMs = 1;
    }

    /** executeTest answers with the run in {@code status}, and so does each later read of it. */
    private void benchmark(TaskStatus status) {
        TestRun run = created.withStatus(status);
        when(orchestrator.testOrchestrator.executeTest(any())).thenReturn(Result.success(run));
        when(orchestrator.testOrchestrator.refreshStatus(id)).thenReturn(run);
    }

    private ResilienceReport execute() {
        ResilienceTestRequest request = new ResilienceTestRequest();
        request.setTestRequest(new CreateTestRequest());
        request.setSteadyStateSec(0);
        request.setMaxRecoveryWaitSec(5);
        request.setProbes(List.of(ProbeSpec.builder("up").build()));
        request.setChaosSpec(FaultSpec.builder("pod-kill")
                .targetNamespace("kafka")
                .chaosDurationSec(1)
                .build());
        return orchestrator.execute(request);
    }

    @Test
    void aCompletedReportNamesItsRun() {
        benchmark(TaskStatus.RUNNING);

        ResilienceReport report = execute();

        assertEquals("COMPLETED", report.getStatus());
        assertEquals(id, report.getTestRunId());
    }

    /** The fault's future failing, as when it gives no answer in time, ends the report ERROR mid-run. */
    @Test
    void aReportThatErredAfterTheRunStartedNamesIt() {
        benchmark(TaskStatus.RUNNING);
        fault = CompletableFuture.failedFuture(new TimeoutException());

        ResilienceReport report = execute();

        assertEquals("ERROR", report.getStatus());
        assertNull(report.getPerformanceReport());
        assertEquals(id, report.getTestRunId());
    }

    @Test
    void aReportRefusedBecauseTheRunEndedNamesIt() {
        benchmark(TaskStatus.FAILED);

        ResilienceReport report = execute();

        assertEquals("ERROR", report.getStatus());
        assertEquals(id, report.getTestRunId());
    }

    @Test
    void anInterruptedReportNamesItsRun() {
        benchmark(TaskStatus.PENDING);

        ResilienceReport report;
        // Interrupted, the wait for the run's tasks to be submitted throws at once.
        Thread.currentThread().interrupt();
        try {
            report = execute();
        } finally {
            Thread.interrupted();
        }

        assertEquals("INTERRUPTED", report.getStatus());
        assertEquals(id, report.getTestRunId());
    }

    @Test
    void aBenchmarkThatDidNotStartHasNoRunToName() {
        when(orchestrator.testOrchestrator.executeTest(any()))
                .thenReturn(Result.failure(new IllegalStateException("Concurrency limit reached")));

        ResilienceReport report = execute();

        assertEquals("ERROR", report.getStatus());
        assertNull(report.getTestRunId());
    }
}
