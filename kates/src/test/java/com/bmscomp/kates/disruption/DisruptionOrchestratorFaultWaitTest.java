package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

import java.time.Instant;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;

import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.ChaosCoordinator;
import com.bmscomp.kates.chaos.ChaosOutcome;
import com.bmscomp.kates.chaos.DisruptionType;
import com.bmscomp.kates.chaos.FaultSpec;
import com.bmscomp.kates.chaos.K8sPodWatcher;
import com.bmscomp.kates.chaos.StrimziStateTracker;
import com.bmscomp.kates.engine.KatesMetrics;

/**
 * The kubernetes provider waits out a fault's delayBeforeSec before it
 * injects. A step that waited chaosDurationSec plus two minutes for the
 * outcome gave up first and went on, and the fault went in later, unwatched.
 */
class DisruptionOrchestratorFaultWaitTest {

    @Test
    void aStepWaitsForTheFaultsDelayAsWellAsItsDuration() throws Exception {
        DisruptionOrchestrator orchestrator = new DisruptionOrchestrator();
        orchestrator.chaosCoordinator = mock(ChaosCoordinator.class);
        orchestrator.podWatcher = mock(K8sPodWatcher.class);
        orchestrator.strimziTracker = mock(StrimziStateTracker.class);
        orchestrator.intelligence = mock(KafkaIntelligenceService.class);
        orchestrator.safetyGuard = mock(DisruptionSafetyGuard.class);
        orchestrator.prometheusCapture = mock(PrometheusMetricsCapture.class);
        orchestrator.slaGrader = mock(SlaGrader.class);
        orchestrator.eventBus = mock(DisruptionEventBus.class);
        orchestrator.katesMetrics = mock(KatesMetrics.class);
        orchestrator.concurrencyGuard = new DisruptionConcurrencyGuard();
        when(orchestrator.safetyGuard.validatePlan(any()))
                .thenReturn(DisruptionSafetyGuard.ValidationResult.ok(List.of()));
        when(orchestrator.safetyGuard.verifyClusterState()).thenReturn(true);
        when(orchestrator.podWatcher.startWatching(any(), any())).thenReturn(mock(K8sPodWatcher.WatchSession.class));
        Instant now = Instant.now();
        CompletableFuture<ChaosOutcome> fault = spy(CompletableFuture.completedFuture(
                ChaosOutcome.success("engine", "kill", now, now, System.nanoTime(), null, null, null)));
        when(orchestrator.chaosCoordinator.triggerFault(any())).thenReturn(fault);

        DisruptionPlan plan = new DisruptionPlan();
        plan.setName("delayed-kill");
        plan.getSteps()
                .add(new DisruptionPlan.DisruptionStep(
                        "kill",
                        FaultSpec.builder("kill")
                                .disruptionType(DisruptionType.POD_KILL)
                                .delayBeforeSec(300)
                                .chaosDurationSec(30)
                                .build(),
                        0,
                        0,
                        false));

        assertEquals("COMPLETED", orchestrator.execute(plan).getStatus());
        verify(fault).get(300 + 30 + 120, TimeUnit.SECONDS);
    }
}
