package com.bmscomp.kates.resilience;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

import java.time.Instant;
import java.util.List;
import java.util.concurrent.CompletableFuture;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
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
 * A run's recoveryTime is set only when a poll of the recovery wait finds
 * every probe passing. A wait with no such poll used to report the time it
 * had run for all the same, a recovery that never came, and a wait under 5 s
 * made no poll at all and reported a recovery of about 0 ms.
 */
class ResilienceOrchestratorRecoveryTest {

    private static final List<ProbeResult> BASELINE = List.of(ProbeResult.pass("isr", "0", 1));
    private static final List<ProbeResult> FAILING = List.of(ProbeResult.fail("isr", "61", 1));
    private static final List<ProbeResult> PASSING = List.of(ProbeResult.pass("isr", "3", 1));

    private final ResilienceOrchestrator orchestrator = new ResilienceOrchestrator();

    @BeforeEach
    void wire() {
        TestRun running = new TestRun(TestType.LOAD, new TestSpec()).withStatus(TaskStatus.RUNNING);
        orchestrator.testOrchestrator = mock(TestOrchestrator.class);
        when(orchestrator.testOrchestrator.executeTest(any())).thenReturn(Result.success(running));
        when(orchestrator.testOrchestrator.refreshStatus(running.getId())).thenReturn(running);

        // Each call that answers a future gets the fault's outcome: these tests
        // are about the wait after the fault, not about how it is triggered.
        Instant now = Instant.now();
        ChaosOutcome outcome =
                ChaosOutcome.success("engine", "pod-kill", now, now, System.nanoTime(), null, null, null);
        orchestrator.chaosCoordinator = mock(
                ChaosCoordinator.class,
                call -> call.getMethod().getReturnType() == CompletableFuture.class
                        ? CompletableFuture.completedFuture(outcome)
                        : Answers.RETURNS_DEFAULTS.answer(call));

        orchestrator.reportGenerator = mock(ReportGenerator.class);
        when(orchestrator.reportGenerator.generate(any())).thenReturn(new TestReport());

        orchestrator.probeExecutor = mock(ProbeExecutor.class);
        orchestrator.recoveryPollIntervalMs = 1;
    }

    /** An Edge probe, so none runs during the fault: the probes' answers are the baseline's, then each poll's. */
    private ResilienceReport execute(int maxRecoveryWaitSec) {
        ResilienceTestRequest request = new ResilienceTestRequest();
        request.setTestRequest(new CreateTestRequest());
        request.setSteadyStateSec(0);
        request.setMaxRecoveryWaitSec(maxRecoveryWaitSec);
        request.setProbes(List.of(ProbeSpec.builder("isr").build()));
        request.setChaosSpec(FaultSpec.builder("pod-kill")
                .targetNamespace("kafka")
                .chaosDurationSec(1)
                .build());
        return orchestrator.execute(request);
    }

    @Test
    void theFirstPollInWhichEveryProbePassesIsTheRecovery() {
        when(orchestrator.probeExecutor.evaluateAll(any(), any())).thenReturn(BASELINE, FAILING, PASSING, FAILING);

        ResilienceReport report = execute(120);

        assertEquals("COMPLETED", report.getStatus());
        assertNotNull(report.getRecoveryTime());
        assertNull(report.getUnrecoveredAfter());
        // That poll is the post-recovery result: nothing is evaluated after it.
        assertEquals(PASSING, report.getPostRecoveryProbes());
        verify(orchestrator.probeExecutor, times(3)).evaluateAll(any(), any());
    }

    @Test
    void aWaitInWhichNoPollPassesIsNoRecovery() {
        when(orchestrator.probeExecutor.evaluateAll(any(), any())).thenReturn(BASELINE, FAILING);

        ResilienceReport report = execute(15);

        assertEquals("COMPLETED", report.getStatus());
        assertNull(report.getRecoveryTime(), "the wait ran out, and the report has a recovery time");
        assertNotNull(report.getUnrecoveredAfter());
        assertEquals(FAILING, report.getPostRecoveryProbes());
        // 15 s is 3 polls of 5 s.
        verify(orchestrator.probeExecutor, times(1 + 3)).evaluateAll(any(), any());
    }

    @ParameterizedTest
    @ValueSource(ints = {-1, 0, 4})
    void aWaitUnderFiveSecondsPollsOnce(int maxRecoveryWaitSec) {
        when(orchestrator.probeExecutor.evaluateAll(any(), any())).thenReturn(BASELINE, FAILING, PASSING);

        ResilienceReport report = execute(maxRecoveryWaitSec);

        assertNull(report.getRecoveryTime(), "no poll passed, and the report has a recovery time");
        assertNotNull(report.getUnrecoveredAfter());
        assertEquals(FAILING, report.getPostRecoveryProbes());
        verify(orchestrator.probeExecutor, times(2)).evaluateAll(any(), any());
    }

    /**
     * An interrupt during the wait used to end it like a wait that ran out,
     * and its time went in the report as the recovery time.
     */
    @Test
    void anInterruptedWaitReportsNoRecovery() {
        when(orchestrator.probeExecutor.evaluateAll(any(), any()))
                .thenReturn(BASELINE)
                .thenAnswer(poll -> {
                    Thread.currentThread().interrupt();
                    return FAILING;
                });

        ResilienceReport report;
        try {
            report = execute(15);
        } finally {
            // The run interrupts its thread again; the next test runs on it too.
            Thread.interrupted();
        }

        assertEquals("INTERRUPTED", report.getStatus());
        assertNull(report.getRecoveryTime());
        assertNull(report.getUnrecoveredAfter());
        assertNull(report.getPostRecoveryProbes());
    }
}
