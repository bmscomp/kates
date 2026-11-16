package com.bmscomp.kates.quorum;

import java.sql.Connection;
import java.sql.SQLException;
import java.time.Duration;
import java.time.Instant;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.agroal.api.AgroalDataSource;
import io.quarkus.scheduler.Scheduled;
import org.jboss.logging.Logger;

import com.bmscomp.kates.disruption.DisruptionConcurrencyGuard;
import com.bmscomp.kates.engine.TestOrchestrator;

/**
 * A leader that cannot reach PostgreSQL hands leadership on (J4d, section 4.6 of
 * plans/j4-resilient-quorum.md): every write it takes fails, while another member may reach the
 * database. After 15 s without it, once nothing is live or after 2 minutes, since handing over
 * fails a live run. At most once per 2 minutes, so members that all lack the database do not pass
 * it back and forth. A transfer that failed is no hand-over: the next check, 5 s on, asks the
 * member after the one that failed, which may not reach its peers yet, as after a partition.
 */
@ApplicationScoped
public class DatabaseHandover {

    private static final Logger LOG = Logger.getLogger(DatabaseHandover.class);
    static final Duration AFTER = Duration.ofSeconds(15);
    static final Duration LIVE_WAIT = Duration.ofMinutes(2);
    static final Duration AGAIN_AFTER = Duration.ofMinutes(2);
    private static final Duration TRANSFER_TIMEOUT = Duration.ofSeconds(10);

    @Inject
    Quorum quorum;

    @Inject
    AgroalDataSource database;

    @Inject
    TestOrchestrator runs;

    @Inject
    DisruptionConcurrencyGuard faults;

    private Instant unreachableSince;
    private Instant handedOverAt;
    private int failedTransfers;

    @Scheduled(
            every = "5s",
            identity = "quorum-database-handover",
            concurrentExecution = Scheduled.ConcurrentExecution.SKIP)
    void check() {
        if (quorum.memberId() == null || !quorum.isLeader()) {
            unreachableSince = null;
            return;
        }
        Instant now = Instant.now();
        if (!decide(now, reachable(), runs.liveRuns() + faults.inFlight())) {
            return;
        }
        try {
            String to = quorum.handOver(TRANSFER_TIMEOUT, failedTransfers);
            if (quorum.isLeader()) {
                // The target could not win its election, and this member won the next one.
                transferFailed();
                LOG.warnf(
                        "PostgreSQL unreachable since %s: handed to %s, but this member leads again",
                        unreachableSince, to);
                return;
            }
            failedTransfers = 0;
            LOG.warnf("PostgreSQL unreachable since %s: leadership handed to %s", unreachableSince, to);
        } catch (java.io.IOException | RuntimeException e) {
            transferFailed();
            LOG.warnf("PostgreSQL unreachable since %s, and handing leadership on failed: %s", unreachableSince, e);
        }
    }

    /** Whether to hand over now, given whether the database answered and how much is live. */
    boolean decide(Instant now, boolean reachable, int live) {
        if (reachable) {
            unreachableSince = null;
            return false;
        }
        if (unreachableSince == null) {
            unreachableSince = now;
            LOG.warnf("Leading without PostgreSQL; handing over after %s", AFTER);
        }
        Duration without = Duration.between(unreachableSince, now);
        boolean due = without.compareTo(AFTER) >= 0 && (live == 0 || without.compareTo(LIVE_WAIT) >= 0);
        boolean rested =
                handedOverAt == null || Duration.between(handedOverAt, now).compareTo(AGAIN_AFTER) >= 0;
        if (due && rested) {
            handedOverAt = now;
            return true;
        }
        return false;
    }

    /** A transfer that failed: still leading, so the next check may try another member. */
    void transferFailed() {
        handedOverAt = null;
        failedTransfers++;
    }

    private boolean reachable() {
        try (Connection connection = database.getConnection()) {
            return connection.isValid(2);
        } catch (SQLException e) {
            return false;
        }
    }
}
