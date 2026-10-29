package com.bmscomp.kates.resilience;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

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
import com.bmscomp.kates.util.Result;

/**
 * A fault whose future fails or times out ended the run ERROR, but the
 * continuous probes kept exec'ing into broker pods forever, and the lease was
 * released with the fault possibly still in the cluster.
 */
class ResilienceOrchestratorFaultErrorTest {

    private final ResilienceOrchestrator orchestrator = new ResilienceOrchestrator();

    private final DisruptionConcurrencyGuard guard = new DisruptionConcurrencyGuard();

    private final String target = guard.currentTarget();

    private final AtomicInteger probeCalls = new AtomicInteger();

    private final CountDownLatch probing = new CountDownLatch(1);

    /** In order: cleanup and whether the lease was still held then. */
    private final List<String> seen = new CopyOnWriteArrayList<>();

    @BeforeEach
    void wire() {
        TestRun running = new TestRun(TestType.LOAD, new TestSpec()).withStatus(TaskStatus.RUNNING);
        orchestrator.testOrchestrator = mock(TestOrchestrator.class);
        when(orchestrator.testOrchestrator.executeTest(any())).thenReturn(Result.success(running));
        when(orchestrator.testOrchestrator.refreshStatus(running.getId())).thenReturn(running);

        // The fault fails once the probe monitor has started.
        orchestrator.chaosCoordinator = mock(ChaosCoordinator.class);
        when(orchestrator.chaosCoordinator.triggerFault(any(), any())).thenAnswer(invocation -> {
            CompletableFuture<ChaosOutcome> future = new CompletableFuture<>();
            CompletableFuture.runAsync(() -> {
                try {
                    probing.await(5, TimeUnit.SECONDS);
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                }
                future.completeExceptionally(new IllegalStateException("the provider broke"));
            });
            return future;
        });
        doAnswer(invocation -> {
                    seen.add("cleanup leased=" + guard.isBusy(target));
                    return null;
                })
                .when(orchestrator.chaosCoordinator)
                .cleanup(any());

        orchestrator.probeExecutor = mock(ProbeExecutor.class);
        when(orchestrator.probeExecutor.evaluateAll(any(), any())).thenAnswer(invocation -> {
            probeCalls.incrementAndGet();
            probing.countDown();
            return List.of(ProbeResult.pass("up", "", 1));
        });

        orchestrator.concurrencyGuard = guard;
    }

    private ResilienceReport execute() {
        ResilienceTestRequest request = new ResilienceTestRequest();
        request.setTestRequest(new CreateTestRequest());
        request.setSteadyStateSec(0);
        request.setMaxRecoveryWaitSec(5);
        request.setProbes(List.of(
                ProbeSpec.builder("up").mode("Continuous").intervalSec(1).build()));
        request.setChaosSpec(FaultSpec.builder("pod-kill")
                .targetNamespace("kafka")
                .chaosDurationSec(1)
                .build());
        return orchestrator.execute(request);
    }

    @Test
    void aFailedFaultStopsTheProbesAndIsCleanedUpBeforeTheLeaseGoes() throws InterruptedException {
        ResilienceReport report = execute();

        assertEquals("ERROR", report.getStatus());
        assertTrue(report.getError().contains("the provider broke"), report.getError());

        // One loop iteration may still be sleeping; none starts after it.
        Thread.sleep(1_500);
        int settled = probeCalls.get();
        Thread.sleep(2_500);
        assertEquals(settled, probeCalls.get(), "the probe monitor kept probing after the run ended");

        assertEquals(List.of("cleanup leased=true"), seen, "cleaned up while the lease was still held");
        assertFalse(guard.isBusy(target), "released after the cleanup");
    }

    @Test
    void aFailedCleanupIsReported() {
        doThrow(new IllegalStateException("API server unreachable"))
                .when(orchestrator.chaosCoordinator)
                .cleanup(any());

        ResilienceReport report = execute();

        assertEquals("ERROR", report.getStatus());
        assertTrue(report.getError().contains("may still be in the cluster"), report.getError());
        assertTrue(report.getError().contains("API server unreachable"), report.getError());
    }

    @Test
    void anInterruptedWaitCleansUpAndHoldsTheLeaseUntilTheFaultStops() throws Exception {
        CompletableFuture<ChaosOutcome> stillRunning = new CompletableFuture<>();
        when(orchestrator.chaosCoordinator.triggerFault(any(), any())).thenReturn(stillRunning);
        doAnswer(invocation -> {
                    seen.add("cleanup interrupted=" + Thread.currentThread().isInterrupted());
                    return null;
                })
                .when(orchestrator.chaosCoordinator)
                .cleanup(any());

        CompletableFuture<ResilienceReport> report = new CompletableFuture<>();
        CompletableFuture<Boolean> stillInterrupted = new CompletableFuture<>();
        Thread runner = new Thread(() -> {
            report.complete(execute());
            stillInterrupted.complete(Thread.currentThread().isInterrupted());
        });
        runner.start();
        assertTrue(probing.await(5, TimeUnit.SECONDS));
        Thread.sleep(300); // into chaosFuture.get()
        runner.interrupt();

        ResilienceReport done = report.get(5, TimeUnit.SECONDS);
        assertEquals("INTERRUPTED", done.getStatus());
        assertEquals(List.of("cleanup interrupted=false"), seen, "cleanup ran on an interrupted thread");
        assertTrue(stillInterrupted.get(5, TimeUnit.SECONDS), "the interrupt was not restored");
        assertTrue(done.getError().contains("may still be running"), done.getError());

        assertTrue(guard.isBusy(target), "lease released while the fault was still running");
        stillRunning.complete(null);
        assertFalse(guard.isBusy(target), "lease kept after the fault stopped");
    }
}
