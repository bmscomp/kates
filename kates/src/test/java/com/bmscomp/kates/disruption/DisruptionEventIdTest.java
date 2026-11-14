package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.doAnswer;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import java.time.Instant;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.ExecutorService;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.ChaosCoordinator;
import com.bmscomp.kates.chaos.ChaosOutcome;
import com.bmscomp.kates.chaos.DisruptionType;
import com.bmscomp.kates.chaos.FaultSpec;
import com.bmscomp.kates.chaos.K8sPodWatcher;
import com.bmscomp.kates.chaos.StrimziStateTracker;
import com.bmscomp.kates.engine.KatesExecutor;
import com.bmscomp.kates.engine.KatesMetrics;

/**
 * GET /api/disruptions/{id}/stream keeps the events whose disruptionId is the
 * id POST /api/disruptions returned. They carried the plan's name, so a
 * subscriber saw nothing past CONNECTED.
 */
class DisruptionEventIdTest {

    @Test
    void aPlansEventsCarryTheIdItWasLaunchedUnder() {
        DisruptionSafetyGuard guard = mock(DisruptionSafetyGuard.class);
        when(guard.validatePlan(any())).thenReturn(DisruptionSafetyGuard.ValidationResult.ok(List.of()));
        when(guard.verifyClusterState()).thenReturn(true);
        DisruptionConcurrencyGuard concurrency = new DisruptionConcurrencyGuard();
        concurrency.kafkaNamespace = "kafka";
        concurrency.kafkaCluster = "krafter";
        DisruptionEventBus bus = new DisruptionEventBus();

        DisruptionOrchestrator orchestrator = new DisruptionOrchestrator();
        orchestrator.chaosCoordinator = mock(ChaosCoordinator.class);
        orchestrator.podWatcher = mock(K8sPodWatcher.class);
        orchestrator.strimziTracker = mock(StrimziStateTracker.class);
        orchestrator.intelligence = mock(KafkaIntelligenceService.class);
        orchestrator.safetyGuard = guard;
        orchestrator.prometheusCapture = mock(PrometheusMetricsCapture.class);
        orchestrator.slaGrader = mock(SlaGrader.class);
        orchestrator.eventBus = bus;
        orchestrator.katesMetrics = mock(KatesMetrics.class);
        orchestrator.concurrencyGuard = concurrency;
        when(orchestrator.podWatcher.startWatching(any(), any())).thenReturn(mock(K8sPodWatcher.WatchSession.class));
        Instant now = Instant.now();
        when(orchestrator.chaosCoordinator.triggerFault(any(), any()))
                .thenReturn(CompletableFuture.completedFuture(
                        ChaosOutcome.success("engine", "kill", now, now, System.nanoTime(), null, null, null)));

        DisruptionLauncher launcher = new DisruptionLauncher();
        launcher.orchestrator = orchestrator;
        launcher.safetyGuard = guard;
        launcher.concurrencyGuard = concurrency;
        launcher.repository = mock(DisruptionReportRepository.class);
        launcher.objectMapper = new ObjectMapper();
        launcher.executor = mock(KatesExecutor.class);
        List<Runnable> handedOff = new CopyOnWriteArrayList<>();
        ExecutorService handOff = mock(ExecutorService.class);
        doAnswer(call -> handedOff.add(call.getArgument(0))).when(handOff).execute(any());
        when(launcher.executor.get()).thenReturn(handOff);

        DisruptionPlan plan = new DisruptionPlan();
        plan.setName("kill-one");
        plan.getSteps()
                .add(new DisruptionPlan.DisruptionStep(
                        "kill",
                        FaultSpec.builder("kill")
                                .disruptionType(DisruptionType.POD_KILL)
                                .chaosDurationSec(30)
                                .build(),
                        0,
                        0,
                        false));

        List<DisruptionEventBus.DisruptionEvent> events = new CopyOnWriteArrayList<>();
        bus.subscribe(events::add);
        String id = launcher.launch(plan).id();
        handedOff.forEach(Runnable::run);

        assertTrue(events.stream().anyMatch(e -> e.type() == DisruptionEventBus.EventType.STEP_STARTED));
        assertEquals(
                List.of(id),
                events.stream()
                        .map(DisruptionEventBus.DisruptionEvent::disruptionId)
                        .distinct()
                        .toList());
    }
}
