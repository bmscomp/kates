package com.bmscomp.kates.engine;

import static com.bmscomp.kates.engine.InMemoryEngine.taskIds;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.*;

import java.util.List;
import java.util.concurrent.atomic.AtomicBoolean;

import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult.TaskStatus;

/**
 * What deleting a run that is still going leaves behind, what it tells the
 * event stream, and what the reconciler does with a run whose row is gone. A
 * delete used to remove the row and leave the run's concurrency permit taken,
 * and two of the three delete paths its workers running too. The reconciler
 * then found the run gone every 5 s and settled nothing, so deleting
 * max-concurrent-tests running runs made every new run answer 429 until a
 * restart. Nor did anything say the run had ended.
 */
class TestOrchestratorDeleteTest {

    private final InMemoryEngine.RecordingBackend other = new InMemoryEngine.RecordingBackend("other");
    private final InMemoryEngine engine = new InMemoryEngine(other);
    private final TestOrchestrator orchestrator = engine.orchestrator;

    @Test
    @DisplayName("a deleted run stops its tasks through its own backend, gives back its slot and is announced")
    void aDeleteSettlesTheRun() {
        String id = engine.startRunning(other);

        assertTrue(orchestrator.deleteTest(id));

        assertFalse(engine.rows.containsKey(id));
        assertEquals(taskIds(id), other.stopped, "stopped through the backend the run is on");
        assertTrue(engine.backend.stopped.isEmpty(), "and not through the default one");
        verify(engine.benchmarkMetrics, atLeastOnce()).endRun(id);
        engine.assertAnnouncedDeleted(id);
        verify(engine.katesMetrics).recordTestCompleted("LOAD", "failed");
        engine.assertEverySlotFree();
        assertFalse(orchestrator.deleteTest(id), "a second delete finds no run");
    }

    @Test
    @DisplayName("deleting a run that has ended announces nothing")
    void anEndedRunsDeleteAnnouncesNothing() {
        String done = engine.storeEnded(TaskStatus.DONE);
        String failed = engine.storeEnded(TaskStatus.FAILED);

        assertTrue(orchestrator.deleteTest(done));
        assertTrue(orchestrator.deleteTest(failed));

        assertTrue(engine.events.isEmpty(), "no event for a run that had ended: " + engine.events);
        verify(engine.katesMetrics, never()).recordTestCompleted(anyString(), anyString());
    }

    @Test
    @DisplayName("the reconciler settles a run whose row was deleted under it")
    void theReconcilerSettlesARunWhoseRowIsGone() {
        List<String> ids = engine.fill();
        // Deleted without settling, as every delete path used to.
        ids.forEach(engine.rows::remove);

        orchestrator.reconcileActiveRuns();

        for (String id : ids) {
            assertTrue(engine.backend.stopped.containsAll(taskIds(id)), "the tasks of " + id + " are stopped");
            verify(engine.benchmarkMetrics, atLeastOnce()).endRun(id);
        }
        // The next tick has nothing left to settle, and gives nothing back twice.
        orchestrator.reconcileActiveRuns();
        engine.assertEverySlotFree();
    }

    @Test
    @DisplayName("a poll that writes after the delete does not bring the run back")
    void aPollRacingTheDeleteLeavesItDeleted() {
        String id = engine.startRunning();
        AtomicBoolean armed = new AtomicBoolean(true);
        // The delete lands between the poll's read of the row and its write,
        // which the stopped tasks make a terminal one.
        engine.backend.onPoll = handle -> {
            if (armed.getAndSet(false)) {
                orchestrator.deleteTest(id);
            }
        };

        orchestrator.reconcileActiveRuns();

        assertFalse(armed.get(), "the poll raced the delete");
        assertFalse(engine.rows.containsKey(id), "the deleted run stays deleted");
        assertTrue(engine.backend.stopped.containsAll(taskIds(id)));
        assertEquals(
                1,
                engine.failuresOf(id).stream()
                        .filter(e -> "deleted".equals(e.getDetail()))
                        .count(),
                "the delete announces the run's end once");
        engine.assertEverySlotFree();
    }

    @Test
    @DisplayName("a run deleted while its tasks are submitted stays deleted, and its workers stop")
    void aDeleteDuringSubmissionLeavesNoWorkers() {
        AtomicBoolean armed = new AtomicBoolean(true);
        // A run's meters start just before its tasks are submitted, so a
        // delete answered here comes before the workers it has to stop.
        doAnswer(invocation -> {
                    if (armed.getAndSet(false)) {
                        orchestrator.deleteTest(invocation.getArgument(0));
                    }
                    return null;
                })
                .when(engine.benchmarkMetrics)
                .startRun(anyString(), anyString(), anyString());

        String id = orchestrator
                .executeTest(InMemoryEngine.request())
                .asSuccess()
                .orElseThrow()
                .getId();

        // Submission runs on a virtual thread, and ends by stopping what it started.
        InMemoryEngine.await(
                () -> engine.backend.stopped.containsAll(taskIds(id)), "the workers of deleted run " + id + " run on");
        assertEquals(taskIds(id), engine.backend.submitted, "the workers were started after the delete");
        assertFalse(engine.rows.containsKey(id), "the RUNNING write did not bring the run back");
        // Deleted while PENDING; the submission that finds it gone announces nothing more.
        engine.assertAnnouncedDeleted(id);
        engine.assertEverySlotFree();
    }

    @Test
    @DisplayName("a run cancelled and then deleted gives back one slot and is announced once")
    void settlingTwiceGivesBackOneSlot() {
        String id = engine.startRunning();
        orchestrator.cancelTest(id).orElseThrow();

        assertTrue(orchestrator.deleteTest(id));

        List<TestLifecycleEvent> failures = engine.failuresOf(id);
        assertEquals(1, failures.size(), "the cancel announced the end, and the delete adds nothing");
        assertEquals("cancelled", failures.get(0).getDetail());
        engine.assertEverySlotFree();
    }
}
