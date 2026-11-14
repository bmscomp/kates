package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;

import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

/**
 * What GET /api/drain counts as a live run. A settle gives the run's permit
 * back before it stops the run's tasks, so the permits alone read zero while
 * those tasks still run, and an operator replacing the pod then would leave
 * them loading Kafka.
 */
class TestOrchestratorDrainTest {

    private final InMemoryEngine engine = new InMemoryEngine();
    private final TestOrchestrator orchestrator = engine.orchestrator;

    @Test
    @DisplayName("a run being settled counts as live until its tasks are stopped")
    void aSettleCountsUntilItsTasksAreStopped() {
        String id = engine.startRunning();
        List<Integer> liveDuringStop = new CopyOnWriteArrayList<>();
        engine.backend.onStop = handle -> liveDuringStop.add(orchestrator.liveRuns());

        orchestrator.settle(id);

        assertFalse(liveDuringStop.isEmpty(), "the run's tasks were stopped");
        assertTrue(liveDuringStop.stream().allMatch(n -> n == 1), "live while stopping, got " + liveDuringStop);
        assertEquals(0, orchestrator.liveRuns());
        engine.assertEverySlotFree();
    }
}
