package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertInstanceOf;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

import java.time.Instant;
import java.util.List;

import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult.TaskStatus;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.quorum.LeadershipAcquired;

/** What the orchestrator does in a quorum: start runs only as leader, recover only what is its to. */
class TestOrchestratorQuorumTest {

    private final InMemoryEngine engine = new InMemoryEngine();
    private final TestOrchestrator orchestrator = engine.orchestrator;

    @Test
    @DisplayName("a member that does not lead starts no run, keeps no permit, and names the leader")
    void aFollowerStartsNothing() {
        when(engine.quorum.acceptsWrites()).thenReturn(false);
        when(engine.quorum.leaderId()).thenReturn("kates-2");

        Exception refused =
                orchestrator.executeTest(InMemoryEngine.request()).asFailure().orElseThrow();

        assertInstanceOf(NotLeaderException.class, refused);
        assertTrue(refused.getMessage().contains("kates-2"), refused.getMessage());
        assertTrue(engine.backend.submitted.isEmpty(), "nothing was submitted");
        when(engine.quorum.acceptsWrites()).thenReturn(true);
        engine.assertEverySlotFree();
    }

    @Test
    @DisplayName("in a quorum, a start recovers this member's own runs, never another's")
    void aStartRecoversOnlyItsOwnRuns() {
        String own = engine.storeEnded(TaskStatus.RUNNING);
        String other = engine.storeEnded(TaskStatus.RUNNING);
        when(engine.quorum.memberId()).thenReturn("kates-1");
        when(engine.repository.findByStatusAndMember(any(), eq("kates-1"), eq(true)))
                .thenReturn(List.of(engine.rows.get(own)));

        orchestrator.recoverOrphans();

        verify(engine.repository, never()).findByStatus(any());
        assertFailedWith(own, "after server restart");
        assertEquals(TaskStatus.RUNNING, engine.rows.get(other).getStatus(), "another member's run is left");
    }

    @Test
    @DisplayName("a new leader fails the live runs every other member created")
    void aTakeoverFailsAPreviousLeadersRuns() {
        String previous = engine.storeEnded(TaskStatus.RUNNING);
        when(engine.quorum.memberId()).thenReturn("kates-1");
        when(engine.repository.findByStatusAndMember(any(), eq("kates-1"), eq(false)))
                .thenReturn(List.of(engine.rows.get(previous)));

        orchestrator.recoverPreviousLeaders(new LeadershipAcquired(4, Instant.now()));

        assertFailedWith(previous, "when the member running it stopped leading");
        verify(engine.repository, never()).findByStatusAndMember(any(), anyString(), eq(true));
    }

    @Test
    @DisplayName("a takeover with the database down still lets the member lead, and the recovery is retried")
    void aTakeoverWithTheDatabaseDownTakesWrites() {
        String previous = engine.storeEnded(TaskStatus.RUNNING);
        when(engine.quorum.memberId()).thenReturn("kates-1");
        when(engine.repository.findByStatusAndMember(any(), eq("kates-1"), eq(false)))
                .thenThrow(new IllegalStateException("PostgreSQL is down"))
                .thenReturn(List.of(engine.rows.get(previous)));

        org.junit.jupiter.api.Assertions.assertDoesNotThrow(
                () -> orchestrator.recoverPreviousLeaders(new LeadershipAcquired(4, Instant.now())));
        assertEquals(TaskStatus.RUNNING, engine.rows.get(previous).getStatus(), "not recovered yet");

        orchestrator.recoverPreviousLeaders();

        assertFailedWith(previous, "when the member running it stopped leading");
    }

    @Test
    @DisplayName("a member that stops leading stops its own runs and gives their permits back")
    void aDeposedMemberStopsItsRuns() {
        String first = engine.startRunning();
        String second = engine.startRunning();

        orchestrator.onLeadershipLost(new com.bmscomp.kates.quorum.LeadershipLost(4, Instant.now()));

        assertTrue(engine.backend.stopped.containsAll(InMemoryEngine.taskIds(first)), "first run's tasks stopped");
        assertTrue(engine.backend.stopped.containsAll(InMemoryEngine.taskIds(second)), "second run's tasks stopped");
        engine.assertEverySlotFree();
    }

    private void assertFailedWith(String id, String reason) {
        TestRun run = engine.rows.get(id);
        assertEquals(TaskStatus.FAILED, run.getStatus());
        assertTrue(
                run.getResults().stream()
                        .anyMatch(r -> r.getError() != null && r.getError().contains(reason)),
                "a task says why: " + run.getResults());
    }
}
