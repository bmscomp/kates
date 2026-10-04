package com.bmscomp.kates.service;

import java.time.Duration;
import java.time.Instant;
import java.util.UUID;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;

import org.jboss.logging.Logger;

/**
 * Cooperative, DB-backed leases for {@code @Scheduled} jobs that must fire on
 * exactly one replica (schedule evaluators would otherwise trigger duplicate
 * test runs when replicas &gt; 1). Claim is a single atomic upsert:
 * insert wins when no row exists; update wins only when the previous lease
 * expired or we already hold it. Postgres and H2 (PostgreSQL mode) both
 * support {@code ON CONFLICT ... DO UPDATE}.
 */
@ApplicationScoped
public class SchedulerLeaseService {

    private static final Logger LOG = Logger.getLogger(SchedulerLeaseService.class);

    /** Stable per-instance identity for the lifetime of this process. */
    private final String holderId = UUID.randomUUID().toString();

    @Inject
    EntityManager em;

    /**
     * @return true when this instance holds the named lease for the next
     *         {@code ttl}; false when another live replica holds it.
     */
    @Transactional(Transactional.TxType.REQUIRES_NEW)
    public boolean tryAcquire(String name, Duration ttl) {
        try {
            Instant expiresAt = Instant.now().plus(ttl);
            int updated = em.createNativeQuery("INSERT INTO scheduler_leases (name, holder, expires_at)"
                            + " VALUES (:name, :holder, :expiresAt)"
                            + " ON CONFLICT (name) DO UPDATE"
                            + " SET holder = EXCLUDED.holder, expires_at = EXCLUDED.expires_at"
                            + " WHERE scheduler_leases.expires_at < NOW()"
                            + "    OR scheduler_leases.holder = EXCLUDED.holder")
                    .setParameter("name", name)
                    .setParameter("holder", holderId)
                    .setParameter("expiresAt", expiresAt)
                    .executeUpdate();
            return updated > 0;
        } catch (Exception e) {
            // Fail open: a broken lease table must not silently stop the only
            // replica from doing scheduled work. Duplicate work on transient
            // DB errors is the lesser evil.
            LOG.warn("Scheduler lease acquisition failed for '" + name + "' — proceeding without lease", e);
            return true;
        }
    }
}
