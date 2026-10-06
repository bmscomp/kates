package com.bmscomp.kates.resilience;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyLong;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.ArgumentMatchers.longThat;
import static org.mockito.Mockito.*;

import java.time.Instant;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.InOrder;

import com.bmscomp.kates.chaos.ChaosCoordinator;
import com.bmscomp.kates.chaos.ChaosOutcome;
import com.bmscomp.kates.chaos.FaultSpec;
import com.bmscomp.kates.chaos.ProbeExecutor;
import com.bmscomp.kates.chaos.ProbeResult;
import com.bmscomp.kates.chaos.ProbeSpec;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.report.ReportGenerator;
import com.bmscomp.kates.report.TestReport;
import com.bmscomp.kates.util.Result;

/** The resilience run is the one place that knows both the benchmark and the moment of the fault. */
class ResilienceOrchestratorChaosStartTest {

    private final TestRun run =
            new TestRun(TestType.INTEGRITY, new TestSpec()).withStatus(TestResult.TaskStatus.RUNNING);
    private final ResilienceOrchestrator orchestrator = new ResilienceOrchestrator();

    @BeforeEach
    void wire() {
        orchestrator.testOrchestrator = mock(TestOrchestrator.class);
        when(orchestrator.testOrchestrator.executeTest(any())).thenReturn(Result.success(run));
        when(orchestrator.testOrchestrator.refreshStatus(run.getId())).thenReturn(run);

        Instant now = Instant.now();
        orchestrator.chaosCoordinator = mock(ChaosCoordinator.class);
        when(orchestrator.chaosCoordinator.triggerFault(any()))
                .thenReturn(CompletableFuture.completedFuture(
                        ChaosOutcome.success("engine", "pod-kill", now, now, System.nanoTime(), null, null, null)));

        orchestrator.reportGenerator = mock(ReportGenerator.class);
        when(orchestrator.reportGenerator.generate(any())).thenReturn(new TestReport());

        // A passing Edge probe keeps the run off the fixed 10s no-probe wait.
        orchestrator.probeExecutor = mock(ProbeExecutor.class);
        when(orchestrator.probeExecutor.evaluateAll(any(), any())).thenReturn(List.of(ProbeResult.pass("up", "", 1)));
    }

    private ResilienceReport execute() {
        return execute(FaultSpec.builder("pod-kill")
                .targetNamespace("kafka")
                .chaosDurationSec(1)
                .build());
    }

    private ResilienceReport execute(FaultSpec chaosSpec) {
        ResilienceTestRequest request = new ResilienceTestRequest();
        request.setSteadyStateSec(0);
        request.setMaxRecoveryWaitSec(5);
        request.setProbes(List.of(ProbeSpec.builder("up").build()));
        request.setChaosSpec(chaosSpec);
        return orchestrator.execute(request);
    }

    /**
     * The kubernetes provider waits out the delay before it injects. A run that
     * waited chaosDurationSec plus two minutes gave up first, and measured a
     * recovery from a fault that had not gone in yet.
     */
    @Test
    void theWaitForTheFaultCoversItsDelay() throws Exception {
        Instant now = Instant.now();
        CompletableFuture<ChaosOutcome> fault = spy(CompletableFuture.completedFuture(
                ChaosOutcome.success("engine", "pod-kill", now, now, System.nanoTime(), null, null, null)));
        when(orchestrator.chaosCoordinator.triggerFault(any())).thenReturn(fault);

        execute(FaultSpec.builder("pod-kill")
                .targetNamespace("kafka")
                .delayBeforeSec(300)
                .chaosDurationSec(1)
                .build());

        verify(fault).get(300 + 1 + 120, TimeUnit.SECONDS);
    }

    @Test
    void faultStartReachesTheBenchmarkBeforeTheFaultIsTriggered() {
        when(orchestrator.chaosCoordinator.injectsFaults()).thenReturn(true);

        assertEquals("COMPLETED", execute().getStatus());

        InOrder order = inOrder(orchestrator.testOrchestrator, orchestrator.chaosCoordinator);
        order.verify(orchestrator.testOrchestrator).markChaosStart(eq(run.getId()), longThat(n -> n > 0));
        order.verify(orchestrator.chaosCoordinator).triggerFault(any());
    }

    @Test
    void noopInjectsNothingSoThereIsNoFaultToMeasureFrom() {
        when(orchestrator.chaosCoordinator.injectsFaults()).thenReturn(false);

        execute();

        verify(orchestrator.testOrchestrator, never()).markChaosStart(anyString(), anyLong());
    }
}
