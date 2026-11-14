package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.doAnswer;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.ExecutorService;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.engine.KatesExecutor;

/**
 * A plan is counted as fault work from its request until its report is
 * saved, which is after POST /api/disruptions has answered: the count is
 * what holds a drained pod until the fault is undone.
 */
class DisruptionLauncherDrainTest {

    private final List<Runnable> handedOff = new ArrayList<>();
    private DisruptionLauncher launcher;
    private DisruptionPlan plan;

    @BeforeEach
    void setUp() {
        launcher = new DisruptionLauncher();
        launcher.concurrencyGuard = new DisruptionConcurrencyGuard();
        launcher.concurrencyGuard.kafkaNamespace = "kafka";
        launcher.concurrencyGuard.kafkaCluster = "krafter";
        launcher.orchestrator = mock(DisruptionOrchestrator.class);
        launcher.safetyGuard = mock(DisruptionSafetyGuard.class);
        launcher.repository = mock(DisruptionReportRepository.class);
        launcher.objectMapper = new ObjectMapper();
        launcher.executor = mock(KatesExecutor.class);
        ExecutorService handOff = mock(ExecutorService.class);
        doAnswer(call -> handedOff.add(call.getArgument(0))).when(handOff).execute(any());
        when(launcher.executor.get()).thenReturn(handOff);
        plan = new DisruptionPlan();
        plan.setName("kill");
    }

    private void validation(List<String> errors) {
        when(launcher.safetyGuard.validatePlan(any()))
                .thenReturn(new DisruptionSafetyGuard.ValidationResult(errors.isEmpty(), List.of(), errors));
    }

    @Test
    void anAcceptedPlanIsCountedUntilItsReportIsSaved() {
        validation(List.of());
        when(launcher.orchestrator.execute(any())).thenReturn(new DisruptionReport());

        assertTrue(launcher.launch(plan).accepted());
        assertEquals(1, launcher.concurrencyGuard.inFlight());

        handedOff.get(0).run();
        assertEquals(0, launcher.concurrencyGuard.inFlight());
    }

    @Test
    void aPlanThatFailsIsCountedOutOnce() {
        validation(List.of());
        when(launcher.orchestrator.execute(any())).thenThrow(new IllegalStateException("no cluster"));

        assertTrue(launcher.launch(plan).accepted());
        handedOff.get(0).run();

        assertEquals(0, launcher.concurrencyGuard.inFlight());
    }

    @Test
    void aRejectedPlanAndABusyClusterCountNothing() {
        validation(List.of("every broker is hit"));
        assertEquals(DisruptionLauncher.Status.REJECTED, launcher.launch(plan).status());
        assertEquals(0, launcher.concurrencyGuard.inFlight());

        String target = launcher.concurrencyGuard.currentTarget();
        assertTrue(launcher.concurrencyGuard.tryAcquire(target, "other#1"));
        assertEquals(DisruptionLauncher.Status.CONFLICT, launcher.launch(plan).status());
        assertEquals(0, launcher.concurrencyGuard.inFlight());
    }

    @Test
    void aDrainRefusesThePlanBeforeAnythingIsSaved() {
        launcher.concurrencyGuard.setDraining(true);

        assertEquals(DisruptionLauncher.Status.DRAINING, launcher.launch(plan).status());

        assertEquals(0, launcher.concurrencyGuard.inFlight());
        assertTrue(handedOff.isEmpty());
        verify(launcher.safetyGuard, never()).validatePlan(any());
    }
}
