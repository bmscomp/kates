package com.bmscomp.kates.engine;

import static com.bmscomp.kates.engine.InMemoryEngine.taskIds;
import static org.junit.jupiter.api.Assertions.*;

import java.util.List;

import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestResult.TaskStatus;
import com.bmscomp.kates.domain.TestRun;

/**
 * What a shutdown of the Kates API does with the runs in flight. It stopped
 * every task through the default backend, which had never started the tasks
 * of a run on another one: with the default native backend, a Trogdor run's
 * tasks kept producing on the agents once the pod was gone, while the run was
 * stored FAILED with "Server shutdown".
 */
class TestOrchestratorShutdownTest {

    private final InMemoryEngine.RecordingBackend trogdor = new InMemoryEngine.RecordingBackend("trogdor");
    private final InMemoryEngine engine = new InMemoryEngine(trogdor);

    @Test
    void eachTaskIsStoppedThroughItsOwnBackend() {
        String onTrogdor = engine.startRunning(trogdor);
        String onDefault = engine.startRunning();

        engine.orchestrator.shutdown();

        assertEquals(taskIds(onTrogdor), trogdor.stopped, "stopped through the backend the run is on");
        assertEquals(taskIds(onDefault), engine.backend.stopped, "and the default backend stopped only its own");
        for (String id : List.of(onTrogdor, onDefault)) {
            TestRun stored = engine.rows.get(id);
            assertEquals(TaskStatus.FAILED, stored.getStatus());
            assertEquals(
                    List.of("Server shutdown", "Server shutdown"),
                    stored.getResults().stream().map(TestResult::getError).toList());
        }
    }
}
