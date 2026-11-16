package com.bmscomp.kates.quorum;

import java.time.Duration;
import java.time.Instant;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.quarkus.scheduler.Scheduled;
import org.jboss.logging.Logger;

/**
 * A member that follows a leader with an empty log, as one rebuilt on a new volume does, asks that
 * leader to step down: Ratis 3.3.1's leader trusts the index the member matched before it lost its
 * disk and sends it heartbeats only, while the next leader sends it the whole log
 * ({@link QuorumMember#askLeaderToStepDown}). After 15 s, since a member that just started gets
 * the log within a heartbeat; at most once a minute, should the next leader not help either.
 */
@ApplicationScoped
public class EmptyLogCatchUp {

    private static final Logger LOG = Logger.getLogger(EmptyLogCatchUp.class);
    static final Duration AFTER = Duration.ofSeconds(15);
    static final Duration AGAIN_AFTER = Duration.ofMinutes(1);
    private static final Duration STEP_DOWN_TIMEOUT = Duration.ofSeconds(10);

    @Inject
    Quorum quorum;

    private Instant emptySince;
    private Instant askedAt;

    @Scheduled(
            every = "5s",
            identity = "quorum-empty-log-catch-up",
            concurrentExecution = Scheduled.ConcurrentExecution.SKIP)
    void check() {
        QuorumMember current = quorum.member();
        boolean emptyUnderLeader = current != null
                && current.serving()
                && !current.isLeader()
                && current.leaderId() != null
                && current.appliedIndex() < 0;
        if (!decide(Instant.now(), emptyUnderLeader)) {
            return;
        }
        String leader = current.leaderId();
        try {
            current.askLeaderToStepDown(STEP_DOWN_TIMEOUT);
            LOG.warnf(
                    "Empty log under %s since %s: it stepped down, so the next leader sends the log",
                    leader, emptySince);
        } catch (java.io.IOException | RuntimeException e) {
            LOG.warnf("Empty log under %s since %s, and asking it to step down failed: %s", leader, emptySince, e);
        }
    }

    /** Whether to ask the leader to step down now, given whether this member follows it with an empty log. */
    boolean decide(Instant now, boolean emptyUnderLeader) {
        if (!emptyUnderLeader) {
            emptySince = null;
            return false;
        }
        if (emptySince == null) {
            emptySince = now;
        }
        boolean due = Duration.between(emptySince, now).compareTo(AFTER) >= 0;
        boolean rested = askedAt == null || Duration.between(askedAt, now).compareTo(AGAIN_AFTER) >= 0;
        if (due && rested) {
            askedAt = now;
            return true;
        }
        return false;
    }
}
