package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CopyOnWriteArrayList;
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
        ChaosCoordinator coordinator = new ChaosCoordinator(providers(new NoOpChaosProvider(), hybrid()), "hybrid");

        assertEquals("hybrid(kubernetes)", coordinator.activeProviderName());
        assertTrue(coordinator.injectsFaults());
        assertTrue(errors().isEmpty(), "a selected provider is not an error: " + errors());
    }

    @Test
    void providersWithoutAnIdOverrideAreStillSelectedByName() {
        ChaosCoordinator coordinator = new ChaosCoordinator(
                providers(new NoOpChaosProvider(), new StubProvider("kubernetes", true)), "kubernetes");

        assertEquals("kubernetes", coordinator.activeProviderName());
    }

    @Test
    void unknownProviderFallsBackToNoopAndSaysSoAtError() {
        ChaosCoordinator coordinator = new ChaosCoordinator(providers(new NoOpChaosProvider(), hybrid()), "hybird");

        assertEquals("noop", coordinator.activeProviderName());
        assertFalse(coordinator.injectsFaults(), "a fallback injects nothing");
        List<String> errors = errors();
        assertEquals(1, errors.size(), "expected one ERROR, got " + errors);
        assertTrue(errors.getFirst().contains("hybird"), errors.getFirst());
        assertTrue(errors.getFirst().contains("hybrid"), "lists the ids that would have matched: " + errors);
        assertTrue(errors.getFirst().contains("DISABLED"), errors.getFirst());
    }

    @Test
    void unavailableProviderFallsBackToNoopAndSaysSoAtError() {
        ChaosCoordinator coordinator = new ChaosCoordinator(
                providers(new NoOpChaosProvider(), new StubProvider("litmus-crd", false)), "litmus-crd");

        assertEquals("noop", coordinator.activeProviderName());
        List<String> errors = errors();
        assertEquals(1, errors.size(), "expected one ERROR, got " + errors);
        assertTrue(errors.getFirst().contains("litmus-crd"), errors.getFirst());
        assertTrue(errors.getFirst().contains("not available"), errors.getFirst());
    }

    @Test
    void configuredNoopIsNotAnError() {
        ChaosCoordinator coordinator = new ChaosCoordinator(providers(new NoOpChaosProvider(), hybrid()), "noop");

        assertEquals("noop", coordinator.activeProviderName());
        assertTrue(errors().isEmpty(), "noop was asked for: " + errors());
    }

    private static HybridChaosProvider hybrid() {
        // A client whose CRD lookup fails means "Litmus not installed", so the
        // hybrid provider delegates to the kubernetes provider.
        KubernetesChaosProvider kubernetes = mock(KubernetesChaosProvider.class);
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
