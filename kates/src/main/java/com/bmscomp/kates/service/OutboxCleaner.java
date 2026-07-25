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
