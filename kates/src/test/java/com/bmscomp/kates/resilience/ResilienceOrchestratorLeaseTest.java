package com.bmscomp.kates.resilience;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

import java.time.Instant;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CopyOnWriteArrayList;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.ChaosCoordinator;
import com.bmscomp.kates.chaos.ChaosOutcome;
import com.bmscomp.kates.chaos.FaultSpec;
import com.bmscomp.kates.chaos.ProbeExecutor;
import com.bmscomp.kates.chaos.ProbeResult;
import com.bmscomp.kates.chaos.ProbeSpec;
import com.bmscomp.kates.disruption.DisruptionConcurrencyGuard;
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
 * A resilience run's fault takes the lease a disruption plan takes. It went in
 * whatever else was running: a plan killing broker 0 and a resilience run
 * killing broker 1 took down two of three brokers together, and either one's
 * cleanup undid the other's fault.
 */
class ResilienceOrchestratorLeaseTest {

    private final ResilienceOrchestrator orchestrator = new ResilienceOrchestrator();

    private final DisruptionConcurrencyGuard guard = new DisruptionConcurrencyGuard();

    private final String target = guard.currentTarget();

    /** Whether the lease was held, at each probe evaluation and when the fault went in. */
    private final List<String> seen = new CopyOnWriteArrayList<>();

    @BeforeEach
    void wire() {
        TestRun running = new TestRun(TestType.LOAD, new TestSpec()).withStatus(TaskStatus.RUNNING);
        orchestrator.testOrchestrator = mock(TestOrchestrator.class);
        when(orchestrator.testOrchestrator.executeTest(any())).thenReturn(Result.success(running));
        when(orchestrator.testOrchestrator.refreshStatus(running.getId())).thenReturn(running);

        Instant now = Instant.now();
        orchestrator.chaosCoordinator = mock(ChaosCoordinator.class);
        when(orchestrator.chaosCoordinator.triggerFault(any(), any())).thenAnswer(invocation -> {
            seen.add("fault " + guard.isBusy(target));
            return CompletableFuture.completedFuture(
                    ChaosOutcome.success("engine", "pod-kill", now, now, System.nanoTime(), null, null, null));
        });

        orchestrator.reportGenerator = mock(ReportGenerator.class);
        when(orchestrator.reportGenerator.generate(any())).thenReturn(new TestReport());

        // An Edge probe: evaluated for the baseline, then by the recovery wait.
        orchestrator.probeExecutor = mock(ProbeExecutor.class);
        when(orchestrator.probeExecutor.evaluateAll(any(), any())).thenAnswer(invocation -> {
            seen.add("probes " + guard.isBusy(target));
            return List.of(ProbeResult.pass("up", "", 1));
        });

        orchestrator.concurrencyGuard = guard;
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
    void aFaultWhileAnotherDisruptionHoldsTheLeaseIsNotInjected() {
        assertTrue(guard.tryAcquire(target, "leader-cascade#1"));

        ResilienceReport report = execute();

        assertEquals("ERROR", report.getStatus());
        assertEquals(
                "Another disruption is running against " + target + ", so no fault was injected", report.getError());
        verify(orchestrator.chaosCoordinator, never()).triggerFault(any(), any());
        assertEquals("leader-cascade#1", guard.activeToken(target).orElseThrow(), "the plan keeps its lease");
    }

    /** Taken after the baseline probes, and held from the fault to the last poll of the recovery wait. */
    @Test
    void theLeaseIsHeldFromTheFaultThroughRecovery() {
        ResilienceReport report = execute();

        assertEquals("COMPLETED", report.getStatus());
        assertEquals(List.of("probes false", "fault true", "probes true"), seen);
    }

    @Test
    void theLeaseIsReleasedWhenTheRunEndsOrFails() {
        assertEquals("COMPLETED", execute().getStatus());
        assertFalse(guard.isBusy(target), "released after a run that ended");

        doThrow(new IllegalStateException("the provider broke"))
                .when(orchestrator.chaosCoordinator)
                .triggerFault(any(), any());

        ResilienceReport failed = execute();

        assertEquals("ERROR", failed.getStatus());
        assertEquals("the provider broke", failed.getError());
        assertFalse(guard.isBusy(target), "released after a run that failed");
    }
}
