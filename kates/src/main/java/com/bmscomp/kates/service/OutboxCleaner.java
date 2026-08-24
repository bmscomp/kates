package com.bmscomp.kates.service;

import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.UUID;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;

import io.quarkus.scheduler.Scheduled;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import com.bmscomp.kates.persistence.OutboxDeadLetterEntity;
import com.bmscomp.kates.persistence.OutboxEventEntity;

/**
 * Owns the two outbox-adjacent write paths that must run in their OWN
 * transaction: deleting a published event, and pruning the consumer-side
 * idempotency ledger.
 */
@ApplicationScoped
public class OutboxCleaner {

    private static final Logger LOG = Logger.getLogger(OutboxCleaner.class);

    @Inject
    EntityManager em;

    @ConfigProperty(name = "kates.outbox.processed-events-retention-days", defaultValue = "7")
    int processedRetentionDays;

    /**
     * Publish attempts before an event is moved to {@code outbox_dead_letters}.
     * At the default 2s poll interval that is roughly ten minutes of retrying a
     * transient broker problem before giving up on the row.
     */
    @ConfigProperty(name = "kates.outbox.max-attempts", defaultValue = "10")
    int maxAttempts;

    /**
     * Deletes a published event.
     *
     * <p>{@code REQUIRES_NEW} because this runs from the emitter's ack callback,
     * long after the polling transaction committed — there is no ambient
     * transaction to join.
     */
    @Transactional(Transactional.TxType.REQUIRES_NEW)
    public void delete(UUID id) {
        try {
            OutboxEventEntity event = em.find(OutboxEventEntity.class, id);
            if (event != null) {
                em.remove(event);
            }
        } catch (Exception e) {
            // A failed delete only means the event is republished later, which
            // consumers deduplicate — never surface it into the messaging layer.
            LOG.warnf("Could not delete published outbox event %s: %s", id, e.getMessage());
        }
    }

    /**
     * Records a failed publish attempt, and retires the event once it has
     * exhausted {@code kates.outbox.max-attempts}.
     *
     * <p>Without this an event that can never be published — an unparseable
     * payload, a message the broker permanently rejects — is retried on every
     * poll forever. Because the poller reads the OLDEST 50 rows, such a row also
     * permanently occupies one of those 50 slots, so a handful of them starve
     * the outbox while the table keeps growing.
     *
     * <p>Retired events are moved to {@code outbox_dead_letters} rather than
     * deleted: losing an event silently is the one failure mode an outbox exists
     * to prevent.
     *
     * @return true if the event was retired to the dead-letter table
     */
    @Transactional(Transactional.TxType.REQUIRES_NEW)
    public boolean recordFailure(UUID id, String error) {
        try {
            OutboxEventEntity event = em.find(OutboxEventEntity.class, id);
            if (event == null) {
                return false; // Published or retired by another replica.
            }
            event.setAttempts(event.getAttempts() + 1);
            event.setLastError(truncate(error));

            if (event.getAttempts() < maxAttempts) {
                return false;
            }

            em.persist(OutboxDeadLetterEntity.from(event, truncate(error)));
            em.remove(event);
            LOG.errorf(
                    "Outbox event %s (%s) moved to outbox_dead_letters after %d attempts: %s",
                    id, event.getEventType(), event.getAttempts(), error);
            return true;
        } catch (Exception e) {
            // Never surface into the messaging layer: the row simply stays and
            // is retried on the next poll.
            LOG.warnf("Could not record outbox failure for %s: %s", id, e.getMessage());
            return false;
        }
    }

    private static String truncate(String error) {
        if (error == null) {
            return null;
        }
        return error.length() <= 2000 ? error : error.substring(0, 2000) + "…";
    }

    /**
     * Prunes {@code processed_events}, the consumer idempotency ledger.
     *
     * <p>Nothing ever deleted from it, so it grew for the life of the
     * deployment — one row per event forever. Keys older than the retention
     * window cannot match a redelivery that is still in flight, so dropping them
     * is safe.
     */
    @Scheduled(
            every = "{kates.outbox.retention-sweep-interval:1h}",
            identity = "processed-events-retention",
            concurrentExecution = Scheduled.ConcurrentExecution.SKIP)
    @Transactional
    public void pruneProcessedEvents() {
        try {
            Instant cutoff = Instant.now().minus(processedRetentionDays, ChronoUnit.DAYS);
            int deleted = em.createQuery("DELETE FROM ProcessedEventEntity p WHERE p.processedAt < :cutoff")
                    .setParameter("cutoff", cutoff)
                    .executeUpdate();
            if (deleted > 0) {
                LOG.infof("Pruned %d processed_events row(s) older than %d day(s)", deleted, processedRetentionDays);
            }
        } catch (Exception e) {
            LOG.warnf("processed_events retention sweep failed: %s", e.getMessage());
        }
    }
}
