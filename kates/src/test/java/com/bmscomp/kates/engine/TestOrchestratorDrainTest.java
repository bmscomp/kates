package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.doAnswer;

import java.time.Duration;
import java.time.Instant;
import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult.TaskStatus;
import com.bmscomp.kates.domain.TestRun;

/**
 * What GET /api/drain counts as a live run. A settle gives the run's permit
 * back before it stops the run's tasks, and a cancel or the reaper writes the
 * FAILED row only after the settle, so the permits alone read zero while the
 * tasks still run or the row still says RUNNING. An operator replacing the
 * pod then would leave tasks loading Kafka, or a run stored RUNNING.
 */
class TestOrchestratorDrainTest {

    private final InMemoryEngine engine = new InMemoryEngine();
    private final TestOrchestrator orchestrator = engine.orchestrator;

    /** liveRuns() as each conditional write read it, before the write landed. */
    private final List<Integer> liveAtWrite = new CopyOnWriteArrayList<>();

    @BeforeEach
    void recordLiveRunsAtEachWrite() {
        doAnswer(invocation -> {
                    liveAtWrite.add(orchestrator.liveRuns());
                    TestRun run = invocation.getArgument(0);
                    TestRun stored = engine.rows.get(run.getId());
                    if (stored == null || stored.getStatus() != invocation.getArgument(1)) {
                        return false;
                    }
                    engine.rows.put(run.getId(), run);
                    return true;
                })
                .when(engine.repository)
                .saveIfStatus(any(), any());
    }

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

    @Test
    @DisplayName("a cancelled run counts as live, once, until its FAILED row is written")
    void aCancelCountsUntilItsRowIsWritten() {
        String id = engine.startRunning();
        List<Integer> liveDuringStop = new CopyOnWriteArrayList<>();
        engine.backend.onStop = handle -> liveDuringStop.add(orchestrator.liveRuns());
        liveAtWrite.clear();

        assertEquals(
                TaskStatus.FAILED, orchestrator.cancelTest(id).orElseThrow().getStatus());

        assertEquals(List.of(1), liveAtWrite, "live when the FAILED row was written");
        assertTrue(liveDuringStop.stream().allMatch(n -> n == 1), "counted once, got " + liveDuringStop);
        assertEquals(0, orchestrator.liveRuns());
        engine.assertEverySlotFree();
    }

    @Test
    @DisplayName("a reaped run counts as live until its FAILED row is written")
    void aReapCountsUntilItsRowIsWritten() {
        String id = engine.startRunning();
        engine.rows.computeIfPresent(
                id,
                (k, run) -> run.withCreatedAt(
                        Instant.now().minus(Duration.ofDays(1)).toString()));
        doAnswer(invocation -> engine.rows.values().stream()
                        .filter(run -> run.getStatus() == invocation.getArgument(0))
                        .toList())
                .when(engine.repository)
                .findByStatus(any());
        TestTimeoutReaper reaper = new TestTimeoutReaper();
        reaper.repository = engine.repository;
        reaper.orchestrator = orchestrator;
        liveAtWrite.clear();

        reaper.reapStuckTests();

        assertEquals(TaskStatus.FAILED, engine.rows.get(id).getStatus());
        assertEquals(List.of(1), liveAtWrite, "live when the FAILED row was written");
        assertEquals(0, orchestrator.liveRuns());
        engine.assertEverySlotFree();
    }

    @Test
    @DisplayName("a run a drain admitted starts during the drain and counts against it")
    void anAdmittedRunStartsAndCounts() {
        orchestrator.setDraining(true);

        String id = orchestrator
                .executeAdmitted(InMemoryEngine.request())
                .asSuccess()
                .orElseThrow()
                .getId();

        assertEquals(1, orchestrator.liveRuns());
        assertTrue(orchestrator.executeTest(InMemoryEngine.request()).isFailure(), "an unadmitted run is refused");
        orchestrator.settle(id);
        assertEquals(0, orchestrator.liveRuns());
    }

    @Test
    @DisplayName("a deleted run counts as live until its row is gone")
    void aDeleteCountsUntilItsRowIsGone() {
        String id = engine.startRunning();
        List<Integer> liveAtDelete = new CopyOnWriteArrayList<>();
        doAnswer(invocation -> {
                    liveAtDelete.add(orchestrator.liveRuns());
                    return java.util.Optional.ofNullable(engine.rows.remove(invocation.<String>getArgument(0)));
                })
                .when(engine.repository)
                .delete(any());

        assertTrue(orchestrator.deleteTest(id));

        assertEquals(List.of(1), liveAtDelete, "live when the row was deleted");
        assertEquals(0, orchestrator.liveRuns());
        engine.assertEverySlotFree();
    }

    @Test
    @DisplayName("a submission whose final write fails stops the tasks it started, and stops counting")
    void aFailedSubmissionWriteStopsItsTasks() {
        doAnswer(invocation -> {
                    if (invocation.getArgument(1) == TaskStatus.PENDING) {
                        throw new IllegalStateException("database blip");
                    }
                    return false;
                })
                .when(engine.repository)
                .saveIfStatus(any(), any());

        String id = orchestrator
                .executeTest(InMemoryEngine.request())
                .asSuccess()
                .orElseThrow()
                .getId();

        InMemoryEngine.await(
                () -> engine.backend.stopped.containsAll(InMemoryEngine.taskIds(id)),
                "the tasks of " + id + " were left producing with no permit");
        assertEquals(0, orchestrator.liveRuns());
        engine.assertEverySlotFree();
    }
}
