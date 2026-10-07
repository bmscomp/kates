package com.bmscomp.kates.service;

import java.time.Instant;
import java.util.Set;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.engine.TestOrchestrator;

/**
 * Deletes the runs in some statuses created before an instant, the oldest
 * first and at most a limit of them at a time. DELETE /api/tests and
 * {@link TestCleanupScheduler} both prune through it, so a run either one
 * deletes goes the way DELETE /api/tests/{id} deletes it, and gets an audit
 * row.
 *
 * <p>Not transactional, nor are its callers: each delete and each audit row
 * commits on its own. A pass then holds no lock on the runs it has deleted
 * while it deletes the next, and a delete that fails part-way keeps the ones
 * before it, each with its audit row.
 */
@ApplicationScoped
public class RunRetention {

    /**
     * One pass: the runs that matched before it, those it deleted, and those
     * that still match after it.
     */
    public record Pass(long matched, int deleted, long remaining) {}

    private final TestOrchestrator orchestrator;
    private final TestRunRepository repository;
    private final AuditService auditService;

    @Inject
    public RunRetention(TestOrchestrator orchestrator, TestRunRepository repository, AuditService auditService) {
        this.orchestrator = orchestrator;
        this.repository = repository;
        this.auditService = auditService;
    }

    /** How many runs in one of {@code statuses} were created before {@code before}. */
    public long count(Set<TestResult.TaskStatus> statuses, Instant before) {
        return repository.countByStatusCreatedBefore(statuses, before);
    }

    /**
     * Deletes at most {@code limit} of the runs in one of {@code statuses}
     * created before {@code before}, the oldest first, and records each one
     * deleted with {@code auditDetails}.
     */
    public Pass prune(Set<TestResult.TaskStatus> statuses, Instant before, int limit, String auditDetails) {
        return prune(statuses, before, limit, auditDetails, () -> {});
    }

    /**
     * {@link #prune(Set, Instant, int, String)}, calling {@code onDeleted}
     * once each run it deletes has its audit row. A pass that throws returns
     * no {@link Pass}, but the runs it deleted before the throw stay deleted,
     * so a caller that reports totals counts them here.
     */
    public Pass prune(
            Set<TestResult.TaskStatus> statuses, Instant before, int limit, String auditDetails, Runnable onDeleted) {
        long matched = count(statuses, before);
        int deleted = 0;
        for (String id : repository.findIdsByStatusCreatedBefore(statuses, before, limit)) {
            // The single delete, which settles a run that is still going. A
            // run another delete took first is gone: it is not counted, and
            // the delete that took it wrote its row.
            if (orchestrator.deleteTest(id)) {
                auditService.record("DELETE", "test", id, auditDetails);
                deleted++;
                onDeleted.run();
            }
        }
        return new Pass(matched, deleted, count(statuses, before));
    }
}
