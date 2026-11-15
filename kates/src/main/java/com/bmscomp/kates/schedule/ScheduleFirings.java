package com.bmscomp.kates.schedule;

import java.time.Instant;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;

/**
 * Claims a schedule's due time in the database. Two members that both believe
 * they lead, a paused leader and the one that took over, may both tick; the
 * conditional update lets only one of them move last_due to the due time, and
 * only that one fires it.
 */
@ApplicationScoped
public class ScheduleFirings {

    /** The tables that hold schedules; the only table names the claim's SQL takes. */
    public enum Kind {
        TESTS("scheduled_test_runs"),
        DISRUPTIONS("disruption_schedules");

        private final String table;

        Kind(String table) {
            this.table = table;
        }
    }

    @Inject
    EntityManager em;

    /**
     * Moves the schedule's last_due forward to {@code due}; true when this call
     * moved it, and so fires that due time. A database error propagates: the
     * due time then fires nowhere, rather than perhaps twice.
     */
    @Transactional(Transactional.TxType.REQUIRES_NEW)
    public boolean claim(Kind kind, String id, Instant due) {
        return em.createNativeQuery("UPDATE " + kind.table + " SET last_due = :due"
                                + " WHERE id = :id AND (last_due IS NULL OR last_due < :due)")
                        .setParameter("due", due)
                        .setParameter("id", id)
                        .executeUpdate()
                > 0;
    }
}
