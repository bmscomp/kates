package com.bmscomp.kates.resilience;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyLong;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.*;

import java.time.Instant;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;
import java.util.function.LongConsumer;
import jakarta.enterprise.inject.Instance;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;

import com.bmscomp.kates.chaos.ChaosCoordinator;
import com.bmscomp.kates.chaos.ChaosOutcome;
import com.bmscomp.kates.chaos.ChaosProvider;
import com.bmscomp.kates.chaos.FaultLimits;
import com.bmscomp.kates.chaos.FaultSpec;
import com.bmscomp.kates.chaos.NoOpChaosProvider;
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

        orchestrator.chaosCoordinator = mock(ChaosCoordinator.class);
        when(orchestrator.chaosCoordinator.triggerFault(any(), any()))
                .thenReturn(CompletableFuture.completedFuture(passed()));

        orchestrator.reportGenerator = mock(ReportGenerator.class);
        when(orchestrator.reportGenerator.generate(any())).thenReturn(new TestReport());

        // A passing Edge probe keeps the run off the fixed 10s no-probe wait.
        orchestrator.probeExecutor = mock(ProbeExecutor.class);
        when(orchestrator.probeExecutor.evaluateAll(any(), any())).thenReturn(List.of(ProbeResult.pass("up", "", 1)));
    }

    private static ChaosOutcome passed() {
        Instant now = Instant.now();
        return ChaosOutcome.success("engine", "pod-kill", now, now, System.nanoTime(), null, null, null);
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
        CompletableFuture<ChaosOutcome> fault = spy(CompletableFuture.completedFuture(passed()));
        when(orchestrator.chaosCoordinator.triggerFault(any(), any())).thenReturn(fault);

        execute(FaultSpec.builder("pod-kill")
                .targetNamespace("kafka")
                .delayBeforeSec(300)
                .chaosDurationSec(1)
                .build());

        verify(fault).get(300 + 1 + 120, TimeUnit.SECONDS);
    }

    @Test
    void theMomentTheProviderInjectsReachesTheBenchmark() {
        long injectedAt = System.nanoTime();
        when(orchestrator.chaosCoordinator.triggerFault(any(), any())).thenAnswer(invocation -> {
            invocation.<LongConsumer>getArgument(1).accept(injectedAt);
            return CompletableFuture.completedFuture(passed());
        });

        assertEquals("COMPLETED", execute().getStatus());

        verify(orchestrator.testOrchestrator).markChaosStart(run.getId(), injectedAt);
    }

    /**
     * Both chaos providers wait out the delay before they inject. The run
     * marked the fault's start before it asked for the fault, so RPO counted
     * the delay: a record sent during it and then lost read as RPO 0, and an
     * older loss came out short by the delay.
     */
    @Test
    void aDelayedFaultIsMeasuredFromTheEndOfItsDelay() {
        // As the providers do: the delay, then the report, then the fault.
        when(orchestrator.chaosCoordinator.triggerFault(any(), any())).thenAnswer(invocation -> {
            FaultSpec spec = invocation.getArgument(0);
            LongConsumer onInject = invocation.getArgument(1);
            return CompletableFuture.supplyAsync(
                    () -> {
                        onInject.accept(System.nanoTime());
                        return passed();
                    },
                    CompletableFuture.delayedExecutor(spec.delayBeforeSec(), TimeUnit.SECONDS));
        });

        long asked = System.nanoTime();
        assertEquals(
                "COMPLETED",
                execute(FaultSpec.builder("pod-kill")
                                .targetNamespace("kafka")
                                .delayBeforeSec(1)
                                .chaosDurationSec(1)
                                .build())
                        .getStatus());

        ArgumentCaptor<Long> marked = ArgumentCaptor.forClass(Long.class);
        verify(orchestrator.testOrchestrator).markChaosStart(eq(run.getId()), marked.capture());
        assertTrue(marked.getValue() - asked >= TimeUnit.SECONDS.toNanos(1), "marked before the delay was over");
    }

    @Test
    void noopInjectsNothingSoThereIsNoFaultToMeasureFrom() {
        orchestrator.chaosCoordinator =
                new ChaosCoordinator(providers(new NoOpChaosProvider()), "noop", new FaultLimits());

        assertEquals("CHAOS_FAILED", execute().getStatus());

        verify(orchestrator.testOrchestrator, never()).markChaosStart(anyString(), anyLong());
    }

    @SuppressWarnings("unchecked")
    private static Instance<ChaosProvider> providers(ChaosProvider... providers) {
        Instance<ChaosProvider> instance = mock(Instance.class);
        when(instance.iterator()).thenAnswer(invocation -> List.of(providers).iterator());
        return instance;
    }
}
