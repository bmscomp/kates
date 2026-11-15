package com.bmscomp.kates.api;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import java.time.Duration;
import java.time.Instant;
import java.util.Optional;

import org.eclipse.microprofile.health.HealthCheckResponse;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.quorum.Quorum;

/**
 * Liveness decides whether the kubelet KILLS the pod, so it fails only on
 * conditions a restart actually fixes. Heap pressure is reported but advisory:
 * restarting mid-benchmark destroys the run and the diagnosis with it.
 */
class KatesLivenessCheckTest {

    private KatesLivenessCheck check;
    private TestOrchestrator orchestrator;
    private Quorum quorum;

    @BeforeEach
    void setUp() {
        check = new KatesLivenessCheck();
        orchestrator = mock(TestOrchestrator.class);
        check.orchestrator = orchestrator;
        when(orchestrator.activeTestCount()).thenReturn(2);
        when(orchestrator.maxConcurrentTests()).thenReturn(3);
        quorum = mock(Quorum.class);
        check.quorum = quorum;
        when(quorum.notServingSince()).thenReturn(Optional.empty());
        check.livenessAfter = Duration.ofSeconds(30);
        check.minFreeHeapMb = 1;
    }

    @Test
    @DisplayName("a quorum member whose Ratis server stays closed is restarted by the kubelet")
    void downWhenTheQuorumServerStaysClosed() {
        when(quorum.notServingSince()).thenReturn(Optional.of(Instant.now().minusSeconds(31)));

        HealthCheckResponse response = check.call();

        assertEquals(HealthCheckResponse.Status.DOWN, response.getStatus());
        assertTrue(((Number) response.getData().orElseThrow().get("quorumNotServingSec")).longValue() >= 31);
    }

    @Test
    @DisplayName("alive while the member is starting its server again")
    void upWhileTheServerIsBeingRestarted() {
        when(quorum.notServingSince()).thenReturn(Optional.of(Instant.now().minusSeconds(5)));

        assertEquals(HealthCheckResponse.Status.UP, check.call().getStatus());
    }

    @Test
    @DisplayName("alive with no quorum")
    void upWithNoQuorum() {
        assertEquals(HealthCheckResponse.Status.UP, check.call().getStatus());
    }

    @Test
    @DisplayName("alive under normal conditions, and reports active runs")
    void upWhenHealthy() {
        check.minFreeHeapMb = 1;

        HealthCheckResponse response = check.call();

        assertEquals(HealthCheckResponse.Status.UP, response.getStatus());
        assertEquals("2/3", response.getData().orElseThrow().get("activeTests"));
    }

    @Test
    @DisplayName("low heap is reported but never kills the pod")
    void heapPressureIsAdvisoryOnly() {
        // A threshold no JVM can satisfy: heapLow must be true.
        check.minFreeHeapMb = Integer.MAX_VALUE;

        HealthCheckResponse response = check.call();

        assertEquals(true, response.getData().orElseThrow().get("heapLow"));
        assertEquals(
                HealthCheckResponse.Status.UP,
                response.getStatus(),
                "restarting on heap pressure would kill benchmarks mid-run");
    }

    @Test
    @DisplayName("an orchestrator failure does not fail the probe")
    void survivesOrchestratorErrors() {
        check.minFreeHeapMb = 1;
        when(orchestrator.activeTestCount()).thenThrow(new IllegalStateException("not ready"));

        HealthCheckResponse response = check.call();

        assertEquals(HealthCheckResponse.Status.UP, response.getStatus());
        assertEquals("0/0", response.getData().orElseThrow().get("activeTests"));
    }
}
