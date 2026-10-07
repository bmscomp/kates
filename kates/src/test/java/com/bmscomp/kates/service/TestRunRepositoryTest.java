package com.bmscomp.kates.service;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import java.util.Optional;
import jakarta.inject.Inject;
import jakarta.transaction.Transactional;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.quarkus.narayana.jta.QuarkusTransaction;
import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.IntegrityResult;
import com.bmscomp.kates.domain.IntegrityResultFixtures;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.domain.events.TestEvent;
import com.bmscomp.kates.persistence.OutboxEventEntity;

@QuarkusTest
class TestRunRepositoryTest {

    private static final ObjectMapper MAPPER = new ObjectMapper();

    @Inject
    TestRunRepository repository;

    @Inject
    jakarta.persistence.EntityManager em;

    @BeforeEach
    @Transactional
    void setUp() {
        em.createQuery("DELETE FROM TestResultEntity").executeUpdate();
        em.createQuery("DELETE FROM TestRunEntity").executeUpdate();
    }

    @Test
    void saveAndFindById() {
        TestRun run = new TestRun(TestType.LOAD, new TestSpec());
        repository.save(run);

        Optional<TestRun> found = repository.findById(run.getId());
        assertTrue(found.isPresent());
        assertEquals(run.getId(), found.get().getId());
        assertEquals(TestType.LOAD, found.get().getTestType());
    }

    @Test
    void findByIdReturnsEmptyForUnknown() {
        assertTrue(repository.findById("nonexistent").isEmpty());
    }

    @Test
    void findAllReturnsAllRuns() {
        repository.save(new TestRun(TestType.LOAD, new TestSpec()));
        repository.save(new TestRun(TestType.STRESS, new TestSpec()));
        repository.save(new TestRun(TestType.SPIKE, new TestSpec()));

        assertEquals(3, repository.findAll().size());
    }

    @Test
    void findByTypeFiltersCorrectly() {
        repository.save(new TestRun(TestType.LOAD, new TestSpec()));
        repository.save(new TestRun(TestType.LOAD, new TestSpec()));
        repository.save(new TestRun(TestType.STRESS, new TestSpec()));

        List<TestRun> loadRuns = repository.findByType(TestType.LOAD);
        List<TestRun> stressRuns = repository.findByType(TestType.STRESS);
        List<TestRun> spikeRuns = repository.findByType(TestType.SPIKE);

        assertEquals(2, loadRuns.size());
        assertEquals(1, stressRuns.size());
        assertEquals(0, spikeRuns.size());
    }

    @Test
    void deleteRemovesRun() {
        TestRun run = new TestRun(TestType.LOAD, new TestSpec());
        repository.save(run);
        repository.delete(run.getId());

        assertTrue(repository.findById(run.getId()).isEmpty());
        assertEquals(0, repository.findAll().size());
    }

    @Test
    void deleteReturnsTheRunItRemoved() {
        TestRun run = new TestRun(TestType.LOAD, new TestSpec()).withStatus(TestResult.TaskStatus.DONE);
        repository.save(run);

        TestRun deleted = repository.delete(run.getId()).orElseThrow();

        assertEquals(run.getId(), deleted.getId());
        assertEquals(TestResult.TaskStatus.DONE, deleted.getStatus());
        assertTrue(repository.delete(run.getId()).isEmpty());
    }

    /**
     * A run deleted before it ended gets its FAILED ending queued with the
     * delete, for the webhooks. Read inside the delete's transaction: once it
     * commits, the outbox poller may publish the event and remove it.
     */
    @Test
    void deletingARunThatHadNotEndedQueuesItsEnding() {
        TestRun run = new TestRun(TestType.LOAD, new TestSpec()).withStatus(TestResult.TaskStatus.RUNNING);
        repository.save(run);

        List<TestEvent> endings = QuarkusTransaction.requiringNew().call(() -> {
            assertEquals(
                    TestResult.TaskStatus.RUNNING,
                    repository.delete(run.getId()).orElseThrow().getStatus());
            return failuresQueuedFor(run.getId());
        });

        assertEquals(1, endings.size());
        assertEquals("deleted", endings.get(0).getMessage());
        assertEquals("LOAD", endings.get(0).getTestType());
    }

    @Test
    void deletingARunThatHadEndedQueuesNothing() {
        TestRun run = new TestRun(TestType.LOAD, new TestSpec()).withStatus(TestResult.TaskStatus.DONE);
        repository.save(run);

        List<TestEvent> endings = QuarkusTransaction.requiringNew().call(() -> {
            repository.delete(run.getId()).orElseThrow();
            return failuresQueuedFor(run.getId());
        });

        assertTrue(endings.isEmpty(), "a run that ended DONE gets no FAILED ending: " + endings);
    }

    /** The FAILED test.lifecycle events queued for a run and not yet published. */
    private List<TestEvent> failuresQueuedFor(String runId) {
        return em
                .createQuery("SELECT e FROM OutboxEventEntity e WHERE e.aggregateId = :id", OutboxEventEntity.class)
                .setParameter("id", runId)
                .getResultList()
                .stream()
                .map(e -> {
                    try {
                        return MAPPER.readValue(e.getPayload(), TestEvent.class);
                    } catch (com.fasterxml.jackson.core.JsonProcessingException ex) {
                        throw new IllegalStateException(ex);
                    }
                })
                .filter(event -> event.getStatus() == TestResult.TaskStatus.FAILED)
                .toList();
    }

    @Test
    void saveIfPresentUpdatesAStoredRun() {
        TestRun run = new TestRun(TestType.LOAD, new TestSpec());
        repository.save(run);

        assertTrue(repository.saveIfPresent(run.withStatus(TestResult.TaskStatus.RUNNING)));

        assertEquals(
                TestResult.TaskStatus.RUNNING,
                repository.findById(run.getId()).orElseThrow().getStatus());
    }

    /**
     * A run's own writes go through saveIfPresent or saveIfStatus, so a write
     * that read the run before a delete and lands after it cannot insert the
     * deleted run again, as a plain save would. Nothing reads the run before the delete
     * here: the test's session would keep that copy and answer later reads
     * with it.
     */
    @Test
    void saveIfPresentNeverInsertsARun() {
        TestRun run = new TestRun(TestType.LOAD, new TestSpec());
        repository.save(run);
        repository.delete(run.getId());

        assertFalse(repository.saveIfPresent(run.withStatus(TestResult.TaskStatus.RUNNING)));
        assertFalse(repository.saveIfPresent(new TestRun(TestType.LOAD, new TestSpec())), "nor one never stored");

        assertTrue(repository.findById(run.getId()).isEmpty(), "the deleted run stays deleted");
        assertEquals(0, repository.findAll().size());
    }

    @Test
    void saveUpdatesExistingRun() {
        TestRun run = new TestRun(TestType.LOAD, new TestSpec());
        repository.save(run);

        run = run.withStatus(TestResult.TaskStatus.DONE);
        repository.save(run);

        TestRun updated = repository.findById(run.getId()).orElseThrow();
        assertEquals(TestResult.TaskStatus.DONE, updated.getStatus());
        assertEquals(1, repository.findAll().size());
    }

    /**
     * An INTEGRITY task's integrity result is stored with it, so a finished
     * run read back still carries it. It was not stored: every read after the
     * poll that saw the run end had none.
     */
    @Test
    void anIntegrityResultIsStoredWithItsTask() {
        IntegrityResult integrity = IntegrityResultFixtures.full();
        TestRun run = new TestRun(TestType.INTEGRITY, new TestSpec());
        String taskId = run.getId() + "-integrity-0";
        repository.save(run.withStatus(TestResult.TaskStatus.RUNNING)
                .withResults(List.of(new TestResult().withTaskId(taskId).withStatus(TestResult.TaskStatus.RUNNING))));

        TestResult done = new TestResult().withTaskId(taskId).withStatus(TestResult.TaskStatus.DONE);
        assertTrue(repository.saveIfPresent(
                run.withStatus(TestResult.TaskStatus.DONE).withResults(List.of(done.withIntegrity(integrity)))));
        assertEquals(integrity, storedIntegrity(run.getId()));

        // A later write of the task without it, such as one built before the
        // poll that saw the run end, leaves it stored.
        assertTrue(repository.saveIfPresent(
                run.withStatus(TestResult.TaskStatus.DONE).withResults(List.of(done))));
        assertEquals(integrity, storedIntegrity(run.getId()));
    }

    /**
     * The integrity result of a run's first task, read in a transaction of its
     * own: a read outside one would answer from the session's copy of the run,
     * not from the database.
     */
    private IntegrityResult storedIntegrity(String runId) {
        return QuarkusTransaction.requiringNew()
                .call(() -> repository
                        .findById(runId)
                        .orElseThrow()
                        .getResults()
                        .get(0)
                        .getIntegrity());
    }
}
