package com.bmscomp.kates.it;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.time.Duration;
import java.util.function.BooleanSupplier;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;

import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.OutboxPoller;
import com.bmscomp.kates.service.TestRunRepository;

/**
 * Outbox and write-path guarantees against real PostgreSQL (wave-2 P3).
 *
 * <p>These need the real database: the behaviours under test are transaction
 * boundaries, row counts and schema objects, none of which the H2 unit path or a
 * mocked repository can demonstrate.
 */
@QuarkusTest
@TestProfile(OutboxTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
class OutboxIT {

    @Inject
    EntityManager em;

    @Inject
    TestRunRepository repository;

    @Inject
    OutboxPoller poller;

    private long outboxRowsFor(String runId) {
        return ((Number) em.createNativeQuery("SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = :id")
                        .setParameter("id", runId)
                        .getSingleResult())
                .longValue();
    }

    private TestRun newRun() {
        TestSpec spec = new TestSpec();
        spec.setTopic("outbox-it");
        return new TestRun(TestType.LOAD, spec).withStatus(TestResult.TaskStatus.PENDING);
    }

    @Test
    void oneOutboxEventPerStateChangeNotPerSave() {
        TestRun run = newRun();
        repository.save(run);
        em.clear();
        assertEquals(1, outboxRowsFor(run.getId()), "creating the run enqueues one event");

        // The status poll path saves repeatedly WITHOUT changing state. Every
        // one of these used to append another duplicate test.lifecycle event.
        repository.save(run);
        repository.save(run);
        repository.save(run);
        em.clear();
        assertEquals(1, outboxRowsFor(run.getId()), "saves that do not change status must not enqueue events");

        // A real transition does enqueue.
        repository.save(run.withStatus(TestResult.TaskStatus.RUNNING));
        em.clear();
        assertEquals(2, outboxRowsFor(run.getId()), "a genuine state change enqueues exactly one more event");

        repository.save(run.withStatus(TestResult.TaskStatus.DONE));
        em.clear();
        assertEquals(3, outboxRowsFor(run.getId()));
    }

    @Test
    void publishedRowsAreDeletedOnlyAfterTheSendCompletes() {
        TestRun run = newRun();
        repository.save(run);
        em.clear();
        assertTrue(outboxRowsFor(run.getId()) > 0, "event is durable before publication");

        poller.processOutbox();

        // Deletion now happens in the send-completion callback, in its own
        // transaction — so it lands shortly AFTER processOutbox returns rather
        // than inside its transaction. Previously the row was removed before the
        // broker had acknowledged anything, losing the event on a failed send.
        boolean drained = waitUntil(Duration.ofSeconds(30), () -> {
            em.clear();
            return outboxRowsFor(run.getId()) == 0;
        });
        assertTrue(drained, "an acknowledged event is eventually removed from the outbox");
    }

    @Test
    void saveIfStatusRefusesToOverwriteAStateThatMovedOn() {
        TestRun run = newRun().withStatus(TestResult.TaskStatus.RUNNING);
        repository.save(run);
        em.clear();

        // The reaper's path: still RUNNING, so the write is allowed.
        assertTrue(
                repository.saveIfStatus(run.withStatus(TestResult.TaskStatus.FAILED), TestResult.TaskStatus.RUNNING),
                "CAS succeeds while the run is still in the expected state");
        em.clear();
        assertEquals(
                TestResult.TaskStatus.FAILED,
                repository.findById(run.getId()).orElseThrow().getStatus());

        // Now the run is FAILED, so a second reaper sweep expecting RUNNING must
        // not write. This is what stops the reaper clobbering a completion that
        // landed while it was working.
        assertFalse(
                repository.saveIfStatus(run.withStatus(TestResult.TaskStatus.DONE), TestResult.TaskStatus.RUNNING),
                "CAS refuses to write over a state that already moved on");
        em.clear();
        assertEquals(
                TestResult.TaskStatus.FAILED,
                repository.findById(run.getId()).orElseThrow().getStatus(),
                "the rejected write left the row untouched");
    }

    @Test
    void versionColumnIncrementsOnUpdate() {
        TestRun run = newRun();
        repository.save(run);
        em.clear();
        long initial = versionOf(run.getId());

        repository.save(run.withStatus(TestResult.TaskStatus.RUNNING));
        em.clear();
        long after = versionOf(run.getId());

        assertTrue(after > initial, "@Version must advance on update (was " + initial + ", now " + after + ")");
    }

    private long versionOf(String runId) {
        return ((Number) em.createNativeQuery("SELECT version FROM test_runs WHERE id = :id")
                        .setParameter("id", runId)
                        .getSingleResult())
                .longValue();
    }

    @Test
    void updatingResultsDoesNotRecreateChildRows() {
        // P3-2: merge() + orphanRemoval used to DELETE and re-INSERT every child
        // on each save, so the generated ids changed every poll. Stable ids prove
        // the rows are being updated in place.
        TestRun run = newRun().withStatus(TestResult.TaskStatus.RUNNING)
                .withResults(java.util.List.of(new TestResult()
                        .withTaskId("task-a")
                        .withStatus(TestResult.TaskStatus.RUNNING)
                        .withRecordsSent(10)));
        repository.save(run);
        em.clear();

        Object firstId = em.createNativeQuery("SELECT id FROM test_results WHERE task_id = 'task-a'")
                .getSingleResult();

        // A later poll reports progress on the same task.
        repository.save(run.withResults(java.util.List.of(new TestResult()
                .withTaskId("task-a")
                .withStatus(TestResult.TaskStatus.DONE)
                .withRecordsSent(999))));
        em.clear();

        Object secondId = em.createNativeQuery("SELECT id FROM test_results WHERE task_id = 'task-a'")
                .getSingleResult();
        assertEquals(firstId.toString(), secondId.toString(), "the child row is updated in place, not replaced");

        Number records = (Number) em.createNativeQuery("SELECT records_sent FROM test_results WHERE task_id = 'task-a'")
                .getSingleResult();
        assertEquals(999L, records.longValue(), "and it carries the updated values");
    }

    @Test
    void v19SchemaObjectsExist() {
        Number index = (Number)
                em.createNativeQuery("SELECT COUNT(*) FROM pg_indexes WHERE indexname = 'idx_outbox_events_created_at'")
                        .getSingleResult();
        assertEquals(1, index.intValue(), "the outbox poller's ORDER BY created_at needs its index");

        Number versionColumn = (Number) em.createNativeQuery("SELECT COUNT(*) FROM information_schema.columns"
                        + " WHERE table_name = 'test_runs' AND column_name = 'version'")
                .getSingleResult();
        assertEquals(1, versionColumn.intValue(), "optimistic locking needs the version column");
    }

    private static boolean waitUntil(Duration timeout, BooleanSupplier condition) {
        long deadline = System.nanoTime() + timeout.toNanos();
        while (System.nanoTime() < deadline) {
            if (condition.getAsBoolean()) {
                return true;
            }
            try {
                Thread.sleep(200);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                return false;
            }
        }
        return condition.getAsBoolean();
    }
}
