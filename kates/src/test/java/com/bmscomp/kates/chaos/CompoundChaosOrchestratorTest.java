package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.TimeUnit;
import jakarta.enterprise.inject.Instance;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.CompoundChaosOrchestrator.CompoundFault;

/**
 * A compound run calls the providers itself, past the coordinator and the
 * safety guard, so its faults went in with any parameter at all.
 */
class CompoundChaosOrchestratorTest {

    private final List<String> triggered = new CopyOnWriteArrayList<>();

    private CompoundChaosOrchestrator orchestrator;

    @BeforeEach
    @SuppressWarnings("unchecked")
    void wire() {
        orchestrator = new CompoundChaosOrchestrator();
        orchestrator.limits = new FaultLimits();
        ChaosProvider recording = new RecordingProvider(triggered);
        orchestrator.providers = mock(Instance.class);
        when(orchestrator.providers.iterator())
                .thenAnswer(invocation -> List.of(recording).iterator());
    }

    private static CompoundFault fault(FaultSpec spec) {
        return new CompoundFault(spec, "recording");
    }

    private static final FaultSpec KILL =
            FaultSpec.builder("kill").disruptionType(DisruptionType.POD_KILL).build();

    private static final FaultSpec FOREVER = FaultSpec.builder("split")
            .disruptionType(DisruptionType.NETWORK_PARTITION)
            .chaosDurationSec(0)
            .build();

    @Test
    void oneFaultOutsideTheLimitsStopsTheRunBeforeAnyFaultGoesIn() {
        List<CompoundFault> faults = List.of(fault(KILL), fault(FOREVER));

        for (var run : List.<Runnable>of(
                () -> orchestrator.executeConcurrent(faults, 5), () -> orchestrator.executeSequential(faults, 0))) {
            IllegalArgumentException refused = assertThrows(IllegalArgumentException.class, run::run);
            assertEquals(
                    "No fault was triggered: faults[1].faultSpec.chaosDurationSec 0 is below 1, and a"
                            + " NETWORK_PARTITION is undone only when its duration ends",
                    refused.getMessage());
        }
        assertEquals(List.of(), triggered, "the pod kill listed first did not go in either");
    }

    @Test
    void aFaultWithoutASpecIsRefusedByItsPlace() {
        List<CompoundFault> faults = new ArrayList<>();
        faults.add(fault(KILL));
        faults.add(fault(null));

        IllegalArgumentException refused =
                assertThrows(IllegalArgumentException.class, () -> orchestrator.executeConcurrent(faults, 5));

        assertEquals("No fault was triggered: faults[1].faultSpec is required", refused.getMessage());
        assertEquals(List.of(), triggered);
    }

    @Test
    void faultsWithinTheLimitsAllGoIn() {
        var outcome = orchestrator.executeConcurrent(List.of(fault(KILL), fault(KILL)), 5);

        assertTrue(outcome.allSucceeded(), outcome.toString());
        assertEquals(List.of("kill", "kill"), triggered);
    }

    /**
     * Each fault of a sequential run was waited for two minutes, whatever its
     * delay and duration. A longer one was reported failed while it still ran,
     * and the next fault went in on top of it.
     */
    @Test
    void aSequentialRunWaitsForEachFaultsDelayAndDurationAsWell() throws Exception {
        CompletableFuture<ChaosOutcome> split = passed("split");
        CompletableFuture<ChaosOutcome> kill = passed("kill");
        ChaosProvider litmus = mock(ChaosProvider.class);
        when(litmus.name()).thenReturn("litmus-crd");
        when(litmus.isAvailable()).thenReturn(true);
        when(litmus.triggerFault(any())).thenReturn(split, kill);
        when(orchestrator.providers.iterator())
                .thenAnswer(invocation -> List.of(litmus).iterator());
        FaultSpec delayedSplit = FaultSpec.builder("split")
                .disruptionType(DisruptionType.NETWORK_PARTITION)
                .delayBeforeSec(300)
                .chaosDurationSec(600)
                .build();

        var outcome = orchestrator.executeSequential(
                List.of(new CompoundFault(delayedSplit, "litmus-crd"), new CompoundFault(KILL, "litmus-crd")), 0);

        assertTrue(outcome.allSucceeded(), outcome.toString());
        verify(split).get(300 + 600 + 120, TimeUnit.SECONDS);
        verify(kill).get(KILL.chaosDurationSec() + 120, TimeUnit.SECONDS);
    }

    private static CompletableFuture<ChaosOutcome> passed(String experimentName) {
        Instant now = Instant.now();
        return spy(CompletableFuture.completedFuture(
                ChaosOutcome.success("engine", experimentName, now, now, System.nanoTime(), null, null, null)));
    }

    private record RecordingProvider(List<String> triggered) implements ChaosProvider {
        @Override
        public String name() {
            return "recording";
        }

        @Override
        public CompletableFuture<ChaosOutcome> triggerFault(FaultSpec spec) {
            triggered.add(spec.experimentName());
            Instant now = Instant.now();
            return CompletableFuture.completedFuture(ChaosOutcome.success(
                    "engine", spec.experimentName(), now, now, System.nanoTime(), null, null, null));
        }

        @Override
        public ChaosStatus pollStatus(String engineName) {
            return ChaosStatus.COMPLETED;
        }

        @Override
        public void cleanup(String engineName) {}

        @Override
        public boolean isAvailable() {
            return true;
        }
    }
}
