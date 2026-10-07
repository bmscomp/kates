package com.bmscomp.kates.service;

import java.util.List;
import java.util.Optional;
import java.util.stream.Collectors;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;

import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.persistence.EntityMapper;
import com.bmscomp.kates.persistence.TestRunEntity;

@ApplicationScoped
public class TestRunRepository {

    @Inject
    EntityManager em;

    /**
     * Shared, thread-safe mapper. A new ObjectMapper was being constructed on
     * every save — each one builds and warms its own serializer cache, so the
     * write path paid that cost per persisted run instead of once per process.
     */
    private static final com.fasterxml.jackson.databind.ObjectMapper OUTBOX_MAPPER =
            new com.fasterxml.jackson.databind.ObjectMapper();

    /**
     * Saves a run only if its persisted status is still {@code expectedCurrent}
     * — a compare-and-set on the run's state.
     *
     * <p>Needed because a plain save is last-write-wins: the timeout reaper
     * decides a run has expired, and by the time it writes FAILED a real
     * completion may already have landed, silently overwriting it. The
     * {@code @Version} column alone does not catch this, since every save
     * re-reads the freshest row and therefore never sees a stale version. This
     * check does, by refusing to write over a state that moved on.
     *
     * @return true if the run was saved, false if another writer changed its
     *     status first (the caller should leave it alone)
     */
    @Transactional
    public boolean saveIfStatus(TestRun run, com.bmscomp.kates.domain.TestResult.TaskStatus expectedCurrent) {
        // Row lock so the status cannot change between this check and the write.
        // save() below runs in THIS transaction (self-invocation, so its own
        // @Transactional does not start a new one) — which is exactly what makes
        // the check and the write atomic.
        TestRunEntity existing =
                em.find(TestRunEntity.class, run.getId(), jakarta.persistence.LockModeType.PESSIMISTIC_WRITE);
        if (existing == null || existing.getStatus() != expectedCurrent) {
            return false;
        }
        save(run);
        return true;
    }

    /**
     * Saves a run only if its row still exists: an update, never an insert.
     *
     * <p>A plain {@link #save} inserts a run it cannot find, so a write that
     * read the run before a delete and landed after it brought the deleted run
     * back. The reconciler's poll and the submission's RUNNING write both could.
     * The row lock makes the check and the write one step against
     * {@link #delete}, which takes the same lock.
     *
     * @return true if the run was saved, false if its row is gone
     */
    @Transactional
    public boolean saveIfPresent(TestRun run) {
        TestRunEntity existing =
                em.find(TestRunEntity.class, run.getId(), jakarta.persistence.LockModeType.PESSIMISTIC_WRITE);
        if (existing == null) {
            return false;
        }
        save(run);
        return true;
    }

    @Transactional
    public void save(TestRun run) {
        TestRunEntity existing = em.find(TestRunEntity.class, run.getId());
        com.bmscomp.kates.domain.TestResult.TaskStatus previousStatus = existing != null ? existing.getStatus() : null;

        if (existing == null) {
            em.persist(EntityMapper.toEntity(run));
        } else {
            // Mutate the MANAGED entity so Hibernate dirty-checks it. merge() of
            // a freshly built graph re-created every child with a null id, which
            // under orphanRemoval deleted and re-inserted the entire results
            // collection on every status poll.
            EntityMapper.updateEntity(existing, run);
        }

        // Enqueue an outbox event only on a real state CHANGE. This used to fire
        // on every save — including each status poll and every reaper pass — so
        // the outbox filled with duplicate test.lifecycle events for runs whose
        // state had not moved, and the poller republished them all.
        if (previousStatus == run.getStatus()) {
            return;
        }
        enqueueLifecycleEvent(run.getId(), run.getTestType(), run.getStatus(), "");
    }

    /** Queues a test.lifecycle event in the current transaction, for the outbox poller to publish. */
    private void enqueueLifecycleEvent(
            String runId, TestType type, com.bmscomp.kates.domain.TestResult.TaskStatus status, String message) {
        try {
            com.bmscomp.kates.domain.events.TestEvent testEvent = new com.bmscomp.kates.domain.events.TestEvent(
                    runId, type != null ? type.name() : "UNKNOWN", status, message, System.currentTimeMillis());
            String payload = OUTBOX_MAPPER.writeValueAsString(testEvent);

            com.bmscomp.kates.persistence.OutboxEventEntity outboxEvent =
                    new com.bmscomp.kates.persistence.OutboxEventEntity(runId, "TestRun", "test.lifecycle", payload);
            em.persist(outboxEvent);
        } catch (Exception e) {
            throw new RuntimeException("Failed to persist outbox event", e);
        }
    }

    public Optional<TestRun> findById(String id) {
        var entity = em.find(TestRunEntity.class, id);
        return entity != null ? Optional.of(EntityMapper.toDomain(entity)) : Optional.empty();
    }

    public List<TestRun> findAll() {
        return em
                .createQuery("SELECT r FROM TestRunEntity r ORDER BY r.createdAt DESC", TestRunEntity.class)
                .getResultList()
                .stream()
                .map(EntityMapper::toDomainSummary)
                .collect(Collectors.toList());
    }

    public List<TestRun> findByType(TestType type) {
        return em
                .createQuery(
                        "SELECT r FROM TestRunEntity r WHERE r.testType = :type ORDER BY r.createdAt DESC",
                        TestRunEntity.class)
                .setParameter("type", type)
                .getResultList()
                .stream()
                .map(EntityMapper::toDomainSummary)
                .collect(Collectors.toList());
    }

    /**
     * Deletes a run and its results.
     *
     * <p>A run deleted before it ended gets the ending no status write will
     * give it: a FAILED test.lifecycle event with the message "deleted",
     * queued in the delete's own transaction, so the webhooks hear the run is
     * over exactly when its row goes. A delete used to queue nothing, so a
     * webhook never heard that a deleted run had ended.
     *
     * <p>The run's row is locked first, as {@link #saveIfPresent} and
     * {@link #saveIfStatus} lock it, so a concurrent write either lands before
     * the delete or finds the row gone. Without the lock a delete could
     * deadlock with a write that holds the run's row and wants its results, or
     * fail on a version that write had just moved. Reading the status under
     * the lock also means a run that ended on its own just before the delete
     * keeps the ending it announced, and gets no second one.
     *
     * @return the run as it was when deleted, or empty when there was none
     *     with this id
     */
    @Transactional
    public Optional<TestRun> delete(String id) {
        var entity = em.find(TestRunEntity.class, id, jakarta.persistence.LockModeType.PESSIMISTIC_WRITE);
        if (entity == null) {
            return Optional.empty();
        }
        TestRun deleted = EntityMapper.toDomainSummary(entity);
        com.bmscomp.kates.domain.TestResult.TaskStatus status = deleted.getStatus();
        if (status != com.bmscomp.kates.domain.TestResult.TaskStatus.DONE
                && status != com.bmscomp.kates.domain.TestResult.TaskStatus.FAILED) {
            enqueueLifecycleEvent(
                    id, deleted.getTestType(), com.bmscomp.kates.domain.TestResult.TaskStatus.FAILED, "deleted");
        }
        em.remove(entity);
        return Optional.of(deleted);
    }

    public List<TestRun> findByLabel(String key, String value) {
        return em
                .createQuery(
                        "SELECT r FROM TestRunEntity r WHERE r.labelsJson LIKE :pattern ORDER BY r.createdAt DESC",
                        TestRunEntity.class)
                .setParameter("pattern", "%" + "\"" + key + "\":\"" + value + "\"" + "%")
                .getResultList()
                .stream()
                .map(EntityMapper::toDomainSummary)
                .collect(Collectors.toList());
    }

    /**
     * JSONB-native label search using PostgreSQL containment operator.
     * Requires the GIN index from V11 migration. Falls back to LIKE-based
     * {@link #findByLabel} if running on H2 (tests).
     */
    @SuppressWarnings("unchecked")
    public List<TestRun> findByLabelJsonb(String key, String value) {
        try {
            String jsonPattern = "{\"" + key + "\":\"" + value + "\"}";
            return ((List<TestRunEntity>) em.createNativeQuery(
                                    "SELECT * FROM test_runs WHERE labels_json @> CAST(:pattern AS jsonb) ORDER BY created_at DESC",
                                    TestRunEntity.class)
                            .setParameter("pattern", jsonPattern)
                            .getResultList())
                    .stream().map(EntityMapper::toDomainSummary).collect(Collectors.toList());
        } catch (Exception e) {
            return findByLabel(key, value);
        }
    }

    public Optional<TestRun> findLatestByType(TestType type) {
        return em
                .createQuery(
                        "SELECT r FROM TestRunEntity r WHERE r.testType = :type ORDER BY r.createdAt DESC",
                        TestRunEntity.class)
                .setParameter("type", type)
                .setMaxResults(1)
                .getResultList()
                .stream()
                .map(EntityMapper::toDomain)
                .findFirst();
    }

    public List<TestRun> findAllPaged(int page, int size) {
        return em
                .createQuery("SELECT r FROM TestRunEntity r ORDER BY r.createdAt DESC", TestRunEntity.class)
                .setFirstResult(page * size)
                .setMaxResults(size)
                .getResultList()
                .stream()
                .map(EntityMapper::toDomainSummary)
                .collect(Collectors.toList());
    }

    public long countAll() {
        return em.createQuery("SELECT COUNT(r) FROM TestRunEntity r", Long.class)
                .getSingleResult();
    }

    public List<TestRun> findByTypePaged(TestType type, int page, int size) {
        return em
                .createQuery(
                        "SELECT r FROM TestRunEntity r WHERE r.testType = :type ORDER BY r.createdAt DESC",
                        TestRunEntity.class)
                .setParameter("type", type)
                .setFirstResult(page * size)
                .setMaxResults(size)
                .getResultList()
                .stream()
                .map(EntityMapper::toDomainSummary)
                .collect(Collectors.toList());
    }

    public long countByType(TestType type) {
        return em.createQuery("SELECT COUNT(r) FROM TestRunEntity r WHERE r.testType = :type", Long.class)
                .setParameter("type", type)
                .getSingleResult();
    }

    public List<TestRun> findByStatusPaged(com.bmscomp.kates.domain.TestResult.TaskStatus status, int page, int size) {
        return em
                .createQuery(
                        "SELECT r FROM TestRunEntity r WHERE r.status = :status ORDER BY r.createdAt DESC",
                        TestRunEntity.class)
                .setParameter("status", status)
                .setFirstResult(page * size)
                .setMaxResults(size)
                .getResultList()
                .stream()
                .map(EntityMapper::toDomainSummary)
                .collect(Collectors.toList());
    }

    public long countByStatus(com.bmscomp.kates.domain.TestResult.TaskStatus status) {
        return em.createQuery("SELECT COUNT(r) FROM TestRunEntity r WHERE r.status = :status", Long.class)
                .setParameter("status", status)
                .getSingleResult();
    }

    /**
     * How many runs in one of {@code statuses} were created before
     * {@code before}: what DELETE /api/tests would prune, and what it left.
     */
    public long countByStatusCreatedBefore(
            java.util.Set<com.bmscomp.kates.domain.TestResult.TaskStatus> statuses, java.time.Instant before) {
        return em.createQuery(
                        "SELECT COUNT(r) FROM TestRunEntity r WHERE r.status IN :statuses AND r.createdAt < :before",
                        Long.class)
                .setParameter("statuses", statuses)
                .setParameter("before", before)
                .getSingleResult();
    }

    /**
     * The ids of the runs in one of {@code statuses} created before
     * {@code before}, oldest first, at most {@code limit} of them.
     *
     * <p>Ids, not runs: DELETE /api/tests deletes each through the
     * orchestrator, which reads the run under its row lock itself. The id
     * orders runs created in the same instant, so a caller that prunes a
     * limit's worth per call goes through them in one order.
     */
    public List<String> findIdsByStatusCreatedBefore(
            java.util.Set<com.bmscomp.kates.domain.TestResult.TaskStatus> statuses,
            java.time.Instant before,
            int limit) {
        return em.createQuery(
                        "SELECT r.id FROM TestRunEntity r WHERE r.status IN :statuses AND r.createdAt < :before"
                                + " ORDER BY r.createdAt ASC, r.id ASC",
                        String.class)
                .setParameter("statuses", statuses)
                .setParameter("before", before)
                .setMaxResults(limit)
                .getResultList();
    }

    /**
     * The runs in this status, read without their task results (see
     * {@link EntityMapper#toDomainSummary}). A caller that changes a run and
     * saves it reads it whole with {@link #findById} first, so that its task
     * results are written back with it.
     */
    public List<TestRun> findByStatus(com.bmscomp.kates.domain.TestResult.TaskStatus status) {
        return em
                .createQuery(
                        "SELECT r FROM TestRunEntity r WHERE r.status = :status ORDER BY r.createdAt DESC",
                        TestRunEntity.class)
                .setParameter("status", status)
                .getResultList()
                .stream()
                .map(EntityMapper::toDomainSummary)
                .collect(Collectors.toList());
    }

    /**
     * The runs of one type and status created in a window, oldest first, each
     * read with its task results, which most list queries here leave out.
     *
     * <p>For callers that measure the runs: a report built from a run without
     * its results is all zeros, which is what trends showed while they read
     * their runs through a date-range query that left them out. The fetch join
     * reads every run's results in this one query, not one query per run.
     * Hibernate returns each run once, however many result rows the join gave
     * it, keeps the ORDER BY, and appends the collection's {@code @OrderBy}, so
     * the results come in the order {@link #findById} reads them. The join is
     * inner: a run without results measured nothing and is left out.
     */
    public List<TestRun> findWithResults(
            TestType type,
            com.bmscomp.kates.domain.TestResult.TaskStatus status,
            java.time.Instant from,
            java.time.Instant to) {
        return em
                .createQuery(
                        "SELECT r FROM TestRunEntity r JOIN FETCH r.results"
                                + " WHERE r.testType = :type AND r.status = :status"
                                + " AND r.createdAt >= :from AND r.createdAt <= :to"
                                + " ORDER BY r.createdAt ASC, r.id ASC",
                        TestRunEntity.class)
                .setParameter("type", type)
                .setParameter("status", status)
                .setParameter("from", from)
                .setParameter("to", to)
                .getResultList()
                .stream()
                .map(EntityMapper::toDomain)
                .collect(Collectors.toList());
    }
}
