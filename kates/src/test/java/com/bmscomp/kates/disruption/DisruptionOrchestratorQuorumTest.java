package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.times;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

import java.time.Instant;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.atomic.AtomicLong;

import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.ChaosCoordinator;
import com.bmscomp.kates.chaos.ChaosOutcome;
import com.bmscomp.kates.chaos.DisruptionType;
import com.bmscomp.kates.chaos.FaultSpec;
import com.bmscomp.kates.chaos.K8sPodWatcher;
import com.bmscomp.kates.chaos.StrimziStateTracker;
import com.bmscomp.kates.engine.KatesMetrics;
import com.bmscomp.kates.quorum.Quorum;

/** A plan belongs to the term it started in, and a member that no longer leads undoes nothing. */
class DisruptionOrchestratorQuorumTest {

    private final DisruptionOrchestrator orchestrator = new DisruptionOrchestrator();
    private final Quorum quorum = mock(Quorum.class);
    private final AtomicLong term = new AtomicLong(4);

    DisruptionOrchestratorQuorumTest() {
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
        orchestrator.quorum = quorum;
        when(quorum.term()).thenAnswer(invocation -> term.get());
        when(quorum.acceptsWrites()).thenReturn(true);
        when(orchestrator.safetyGuard.validatePlan(any()))
                .thenReturn(DisruptionSafetyGuard.ValidationResult.ok(List.of()));
        when(orchestrator.safetyGuard.verifyClusterState()).thenReturn(true);
    }

    private static DisruptionPlan.DisruptionStep kill(String name, boolean requireRecovery) {
        return new DisruptionPlan.DisruptionStep(
                name,
                FaultSpec.builder(name).disruptionType(DisruptionType.POD_KILL).build(),
                0,
                0,
                requireRecovery);
    }

    private static CompletableFuture<ChaosOutcome> passed() {
        Instant now = Instant.now();
        return CompletableFuture.completedFuture(
                ChaosOutcome.success("engine", "kill", now, now, System.nanoTime(), null, null, null));
    }

    @Test
    void aPlanStopsOnceTheTermItStartedInEnds() {
        when(orchestrator.podWatcher.startWatching(any(), any())).thenReturn(mock(K8sPodWatcher.WatchSession.class));
        when(orchestrator.chaosCoordinator.triggerFault(any(), any())).thenAnswer(invocation -> {
            term.set(5); // the member lost the lead and won it back during the first step
            return passed();
        });
        DisruptionPlan plan = new DisruptionPlan();
        plan.setName("two-kills");
        plan.getSteps().add(kill("first", false));
        plan.getSteps().add(kill("second", false));

        orchestrator.execute("d-1", plan);

        verify(orchestrator.chaosCoordinator, times(1)).triggerFault(any(), any());
    }

    @Test
    void aMemberThatNoLongerLeadsDoesNotRollBack() throws Exception {
        K8sPodWatcher.WatchSession session = mock(K8sPodWatcher.WatchSession.class);
        when(session.awaitRecovery(org.mockito.ArgumentMatchers.anyLong(), any())).thenReturn(false);
        when(orchestrator.podWatcher.startWatching(any(), any())).thenReturn(session);
        when(orchestrator.chaosCoordinator.triggerFault(any(), any())).thenAnswer(invocation -> {
            invocation.<java.util.function.LongConsumer>getArgument(1).accept(System.nanoTime());
            // The member loses the lead while its fault is in.
            when(quorum.acceptsWrites()).thenReturn(false);
            return passed();
        });
        DisruptionPlan plan = new DisruptionPlan();
        plan.setName("unrecovered");
        plan.setAutoRollback(true);
        plan.getSteps().add(kill("kill", true));

        orchestrator.execute("d-2", plan);

        verify(orchestrator.safetyGuard, never()).rollback(any(), any());
    }
}
