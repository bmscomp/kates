package com.bmscomp.kates.service;

import java.time.Duration;
import java.time.Instant;
import java.util.EnumSet;
import java.util.Set;
import java.util.concurrent.atomic.AtomicInteger;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.quarkus.scheduler.Scheduled;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import com.bmscomp.kates.domain.TestResult;

/**
 * Deletes the finished runs created more than
 * {@code kates.cleanup.retention-days} ago, through {@link RunRetention}, as
 * DELETE /api/tests does: each run settled and given an audit row. Each
 * replica sweeps when it starts, as an interval with no delay fires at once,
 * and every 24 hours after.
 *
 * <p>The sweep used to read every run, with its results, in one transaction;
 * delete each one older than the cutoff that was neither PENDING nor
 * RUNNING, STOPPING ones too, by its row alone, which neither settled the
 * run nor wrote an audit row; and skip, unlogged, a run whose creation time
 * it could not parse. The database now picks the runs, by the instant
 * stored.
 *
 * <p>There is no lease: two replicas sweeping at once delete each run once.
 * Whichever deletes a run second finds it gone, which
 * {@link com.bmscomp.kates.engine.TestOrchestrator#deleteTest} reports, so
 * it neither counts the run nor writes a row for it.
 */
@ApplicationScoped
public class TestCleanupScheduler {

    private static final Logger LOG = Logger.getLogger(TestCleanupScheduler.class);

    /**
     * DONE and FAILED only. A PENDING, RUNNING or STOPPING run may still have
     * tasks producing, in this replica or another, so it is left alone. The
     * reconciler ends a RUNNING run, or the timeout reaper fails it past its
     * deadline, and a later sweep takes it. Nothing ends a PENDING run whose
     * submission died, nor a STOPPING run an older version left: the reaper
     * and orphan recovery read RUNNING runs only, and the reconciler only
     * this process's handles. Such a run stays until DELETE /api/tests/{id}
     * deletes it.
     */
    private static final Set<TestResult.TaskStatus> FINISHED =
            EnumSet.of(TestResult.TaskStatus.DONE, TestResult.TaskStatus.FAILED);

    /**
     * The most passes one sweep makes: a million runs at the default batch
     * size, far more than a day adds, so the bound only stops a sweep that
     * would otherwise never end.
     */
    private static final int MAX_PASSES = 1000;

    @Inject
    RunRetention retention;

    @ConfigProperty(name = "kates.cleanup.retention-days", defaultValue = "90")
    int retentionDays;

    /**
     * The most runs one pass deletes, as DELETE /api/tests deletes at most a
     * thousand a call. Package-private so a test can lower it.
     */
    int batchSize = 1000;

    @Scheduled(every = "24h", identity = "test-data-cleanup")
    void cleanupOldTests() {
        sweep(Instant.now().minus(Duration.ofDays(retentionDays)));
    }

    /**
     * Deletes the DONE and FAILED runs created before {@code cutoff}, a
     * batch a pass, until none matches, a pass deletes none, or
     * {@link #MAX_PASSES} passes. A pass that throws ends the sweep: it is
     * logged, not thrown, and the next sweep tries again.
     *
     * @return how many runs it deleted, those of a pass that threw included
     */
    int sweep(Instant cutoff) {
        String details =
                "retention sweep: created before " + cutoff + " (kates.cleanup.retention-days=" + retentionDays + ")";
        // Counted as each delete commits, not from what a pass returns: a
        // pass that throws part-way returns nothing, but has deleted, and
        // audited, the runs before the throw.
        AtomicInteger deleted = new AtomicInteger();
        // What still matches after the sweep; null when an error left it unknown.
        Long remaining = null;
        for (int pass = 0; pass < MAX_PASSES; pass++) {
            RunRetention.Pass done;
            try {
                done = retention.prune(FINISHED, cutoff, batchSize, details, deleted::incrementAndGet);
            } catch (RuntimeException e) {
                LOG.errorf(
                        e,
                        "Retention sweep of the runs created before %s stopped after deleting %d: %s",
                        cutoff,
                        deleted.get(),
                        e.getMessage());
                remaining = recount(cutoff);
                break;
            }
            remaining = done.remaining();
            // A pass that deleted none found its runs gone: another replica's
            // sweep, or a delete through the API, is taking them.
            if (remaining == 0 || done.deleted() == 0) {
                break;
            }
        }
        int total = deleted.get();
        if (total > 0) {
            LOG.infof(
                    "Retention sweep deleted %d DONE or FAILED run(s) created before %s"
                            + " (kates.cleanup.retention-days=%d)%s",
                    total, cutoff, retentionDays, remaining == null ? "" : "; " + remaining + " still match");
        }
        return total;
    }

    /**
     * What still matches after a pass that threw, whose own count went with
     * it; null when counting throws too, as it does while the database is
     * down.
     */
    private Long recount(Instant cutoff) {
        try {
            return retention.count(FINISHED, cutoff);
        } catch (RuntimeException e) {
            LOG.warnf(
                    "Retention sweep could not count the runs created before %s still left: %s",
                    cutoff, e.getMessage());
            return null;
        }
    }
}
