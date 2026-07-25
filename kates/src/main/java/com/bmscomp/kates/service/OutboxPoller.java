package com.bmscomp.kates.service;

import java.util.List;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.quarkus.scheduler.Scheduled;
import org.eclipse.microprofile.reactive.messaging.Channel;
import org.eclipse.microprofile.reactive.messaging.Emitter;
import org.jboss.logging.Logger;

import com.bmscomp.kates.domain.events.TestEvent;
import com.bmscomp.kates.persistence.OutboxEventEntity;

/**
 * Publishes transactional-outbox rows to Kafka.
 *
 * <p><b>The row is deleted only after the broker acknowledges.</b> The previous
 * implementation called {@code emitter.send(...)} — which is asynchronous and
 * returns a {@link java.util.concurrent.CompletionStage} — and then removed the
 * row in the same transaction. The transaction committed before the send was
 * acknowledged, so a broker failure after commit destroyed the only record of
 * the event: exactly the at-least-once guarantee the outbox pattern exists to
 * provide. Now a send failure leaves the row in place and the next poll retries
 * it.
 *
 * <p>Retries can therefore publish an event more than once. That is the intended
 * at-least-once semantics, and consumers already deduplicate through the
 * {@code processed_events} ledger.
 */
@ApplicationScoped
public class OutboxPoller {

    private static final Logger LOG = Logger.getLogger(OutboxPoller.class);
    private static final ObjectMapper MAPPER = new ObjectMapper();

    @Inject
    EntityManager em;

    @Inject
    OutboxCleaner cleaner;

    @Channel("test-events-out")
    Emitter<TestEvent> eventEmitter;

    /**
     * Rows dispatched but not yet acknowledged. Without this the next poll — two
     * seconds later — would re-send everything still awaiting an ack, turning a
     * slow broker into a duplicate storm. Purely an optimisation: the set is
     * lost if the process dies, and the surviving rows are then correctly
     * retried.
     */
    private final Set<UUID> inFlight = ConcurrentHashMap.newKeySet();

    @Scheduled(every = "{kates.outbox.poll-interval:2s}", identity = "outbox-poller")
    @Transactional
    public void processOutbox() {
        List<OutboxEventEntity> events = em.createQuery(
                        "SELECT e FROM OutboxEventEntity e ORDER BY e.createdAt ASC", OutboxEventEntity.class)
                .setMaxResults(50)
                .setLockMode(jakarta.persistence.LockModeType.PESSIMISTIC_WRITE)
                .setHint("jakarta.persistence.lock.timeout", -2) // -2 maps to SKIP LOCKED in Hibernate
                .getResultList();

        for (OutboxEventEntity event : events) {
            UUID id = event.getId();
            if (!inFlight.add(id)) {
                continue; // Already dispatched, still awaiting its ack.
            }
            try {
                TestEvent testEvent = MAPPER.readValue(event.getPayload(), TestEvent.class);
                eventEmitter.send(testEvent).whenComplete((ignored, failure) -> {
                    try {
                        if (failure != null) {
                            LOG.errorf("Outbox event %s not acknowledged, will retry: %s", id, failure.getMessage());
                        } else {
                            // Committed to the broker — safe to forget.
                            cleaner.delete(id);
                        }
                    } finally {
                        inFlight.remove(id);
                    }
                });
            } catch (Exception e) {
                inFlight.remove(id);
                LOG.error("Failed to publish outbox event: " + id, e);
            }
        }
    }
}
