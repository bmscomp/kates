package com.bmscomp.kates.service;

import java.util.List;
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

@ApplicationScoped
public class OutboxPoller {

    private static final Logger LOG = Logger.getLogger(OutboxPoller.class);
    private static final ObjectMapper MAPPER = new ObjectMapper();

    @Inject
    EntityManager em;

    @Channel("test-events-out")
    Emitter<TestEvent> eventEmitter;

    @Scheduled(every = "2s")
    @Transactional
    public void processOutbox() {
        List<OutboxEventEntity> events = em.createQuery(
                        "SELECT e FROM OutboxEventEntity e ORDER BY e.createdAt ASC", OutboxEventEntity.class)
                .setMaxResults(50)
                .setLockMode(jakarta.persistence.LockModeType.PESSIMISTIC_WRITE)
                .setHint("jakarta.persistence.lock.timeout", -2) // -2 maps to SKIP LOCKED in Hibernate
                .getResultList();

        for (OutboxEventEntity event : events) {
            try {
                TestEvent testEvent = MAPPER.readValue(event.getPayload(), TestEvent.class);
                eventEmitter.send(testEvent);
                em.remove(event);
            } catch (Exception e) {
                LOG.error("Failed to publish outbox event: " + event.getId(), e);
            }
        }
    }
}
