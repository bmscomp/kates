package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.function.LongConsumer;
import java.util.logging.Handler;
import java.util.logging.Level;
import java.util.logging.LogRecord;
import java.util.logging.SimpleFormatter;
import jakarta.enterprise.inject.Instance;

import io.fabric8.kubernetes.client.KubernetesClient;
import org.jboss.logmanager.ExtLogRecord;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

class ChaosCoordinatorTest {

    /** Held so the logger, and the handler on it, outlive the test's own references. */
    private final java.util.logging.Logger coordinatorLog =
            java.util.logging.Logger.getLogger(ChaosCoordinator.class.getName());

    private final CapturingHandler captured = new CapturingHandler();

    @BeforeEach
    void captureLogs() {
        coordinatorLog.addHandler(captured);
    }

    @AfterEach
    void releaseLogs() {
        coordinatorLog.removeHandler(captured);
    }

    @Test
    void hybridIsSelectedByTheNameTheDocsTellYouToConfigure() {
        // The real class, not a stub: its name() is "hybrid(<delegate>)", which
        // is exactly what made the old name() comparison unable to match.
        ChaosCoordinator coordinator =
                new ChaosCoordinator(providers(new NoOpChaosProvider(), hybrid()), "hybrid", new FaultLimits());

        assertEquals("hybrid(kubernetes)", coordinator.activeProviderName());
        assertTrue(errors().isEmpty(), "a selected provider is not an error: " + errors());
    }

    /** A resilience run on hybrid measures RPO from the moment its delegate injects. */
    @Test
    void hybridHandsTheInjectionReportToTheProviderItDelegatesTo() {
        KubernetesChaosProvider kubernetes = mock(KubernetesChaosProvider.class);
        ChaosCoordinator coordinator = new ChaosCoordinator(
                providers(new NoOpChaosProvider(), hybrid(kubernetes)), "hybrid", new FaultLimits());
        FaultSpec kill = FaultSpec.builder("kill")
                .disruptionType(DisruptionType.POD_KILL)
                .build();
        LongConsumer onInject = injectedAt -> {};

        coordinator.triggerFault(kill, onInject);

        verify(kubernetes).triggerFault(kill, onInject);
    }

    @Test
    void providersWithoutAnIdOverrideAreStillSelectedByName() {
        ChaosCoordinator coordinator = new ChaosCoordinator(
                providers(new NoOpChaosProvider(), new StubProvider("kubernetes", true)),
                "kubernetes",
                new FaultLimits());

        assertEquals("kubernetes", coordinator.activeProviderName());
    }

    @Test
    void unknownProviderFallsBackToNoopAndSaysSoAtError() {
        ChaosCoordinator coordinator =
                new ChaosCoordinator(providers(new NoOpChaosProvider(), hybrid()), "hybird", new FaultLimits());

        assertEquals("noop", coordinator.activeProviderName());
        List<Long> injected = new CopyOnWriteArrayList<>();
        coordinator.triggerFault(FaultSpec.builder("kill").build(), injected::add);
        assertEquals(List.of(), injected, "a fallback injects nothing");
        List<String> errors = errors();
        assertEquals(1, errors.size(), "expected one ERROR, got " + errors);
        assertTrue(errors.getFirst().contains("hybird"), errors.getFirst());
        assertTrue(errors.getFirst().contains("hybrid"), "lists the ids that would have matched: " + errors);
        assertTrue(errors.getFirst().contains("DISABLED"), errors.getFirst());
    }

    @Test
    void unavailableProviderFallsBackToNoopAndSaysSoAtError() {
        ChaosCoordinator coordinator = new ChaosCoordinator(
                providers(new NoOpChaosProvider(), new StubProvider("litmus-crd", false)),
                "litmus-crd",
                new FaultLimits());

        assertEquals("noop", coordinator.activeProviderName());
        List<String> errors = errors();
        assertEquals(1, errors.size(), "expected one ERROR, got " + errors);
        assertTrue(errors.getFirst().contains("litmus-crd"), errors.getFirst());
        assertTrue(errors.getFirst().contains("not available"), errors.getFirst());
    }

    @Test
    void configuredNoopIsNotAnError() {
        ChaosCoordinator coordinator =
                new ChaosCoordinator(providers(new NoOpChaosProvider(), hybrid()), "noop", new FaultLimits());

        assertEquals("noop", coordinator.activeProviderName());
        assertTrue(errors().isEmpty(), "noop was asked for: " + errors());
    }

    /**
     * The check every plan step and resilience run passes. A resilience run
     * never went through the safety guard, and a NETWORK_PARTITION of 0
     * seconds was never removed by the kubernetes provider.
     */
    @Test
    void aFaultOutsideTheLimitsNeverReachesTheProvider() {
        ChaosProvider kubernetes = mock(ChaosProvider.class);
        when(kubernetes.id()).thenReturn("kubernetes");
        when(kubernetes.name()).thenReturn("kubernetes");
        when(kubernetes.isAvailable()).thenReturn(true);
        ChaosCoordinator coordinator =
                new ChaosCoordinator(providers(new NoOpChaosProvider(), kubernetes), "kubernetes", new FaultLimits());
        FaultSpec forever = FaultSpec.builder("split")
                .disruptionType(DisruptionType.NETWORK_PARTITION)
                .chaosDurationSec(0)
                .build();

        IllegalArgumentException refused =
                assertThrows(IllegalArgumentException.class, () -> coordinator.triggerFault(forever));
        // A resilience run asks for its fault with a report of when it goes in.
        assertThrows(IllegalArgumentException.class, () -> coordinator.triggerFault(forever, injectedAt -> {}));

        assertEquals(
                "Fault 'split' is outside the chaos limits: chaosDurationSec 0 is below 1, and a NETWORK_PARTITION"
                        + " is undone only when its duration ends",
                refused.getMessage());
        verify(kubernetes, never()).triggerFault(any());
        verify(kubernetes, never()).triggerFault(any(), any());
    }

    private static HybridChaosProvider hybrid() {
        return hybrid(mock(KubernetesChaosProvider.class));
    }

    private static HybridChaosProvider hybrid(KubernetesChaosProvider kubernetes) {
        // A client whose CRD lookup fails means "Litmus not installed", so the
        // hybrid provider delegates to the kubernetes provider.
        when(kubernetes.isAvailable()).thenReturn(true);
        return new HybridChaosProvider(mock(KubernetesClient.class), mock(LitmusChaosProvider.class), kubernetes);
    }

    @SuppressWarnings("unchecked")
    private static Instance<ChaosProvider> providers(ChaosProvider... providers) {
        Instance<ChaosProvider> instance = mock(Instance.class);
        when(instance.iterator()).thenAnswer(invocation -> List.of(providers).iterator());
        return instance;
    }

    private List<String> errors() {
        return captured.records.stream()
                .filter(r -> r.getLevel().intValue() >= Level.SEVERE.intValue())
                .map(ChaosCoordinatorTest::text)
                .toList();
    }

    private static String text(LogRecord record) {
        return record instanceof ExtLogRecord ext
                ? ext.getFormattedMessage()
                : new SimpleFormatter().formatMessage(record);
    }

    private static final class CapturingHandler extends Handler {
        final List<LogRecord> records = new CopyOnWriteArrayList<>();

        @Override
        public void publish(LogRecord record) {
            records.add(record);
        }

        @Override
        public void flush() {}

        @Override
        public void close() {}
    }

    private record StubProvider(String name, boolean available) implements ChaosProvider {
        @Override
        public CompletableFuture<ChaosOutcome> triggerFault(FaultSpec spec) {
            return CompletableFuture.completedFuture(ChaosOutcome.skipped("stub"));
        }

        @Override
        public ChaosStatus pollStatus(String engineName) {
            return ChaosStatus.NOT_FOUND;
        }

        @Override
        public void cleanup(String engineName) {}

        @Override
        public boolean isAvailable() {
            return available;
        }
    }
}
