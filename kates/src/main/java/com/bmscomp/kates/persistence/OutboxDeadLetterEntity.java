package com.bmscomp.kates.persistence;

import java.time.Instant;
import java.util.UUID;
import jakarta.persistence.*;

/**
 * An outbox event that could not be published after the configured number of
 * attempts.
 *
 * <p>Kept rather than deleted: the outbox exists so an event is never lost, and
 * dropping the ones that fail hardest would quietly break exactly the guarantee
 * it provides. Rows here are for operators — inspect, fix the cause, replay by
 * re-inserting into {@code outbox_events}.
 */
@Entity
@Table(name = "outbox_dead_letters")
public class OutboxDeadLetterEntity {

    @Id
    @Column(name = "id")
    private UUID id;

    @Column(name = "aggregate_id", nullable = false)
    private String aggregateId;

    @Column(name = "aggregate_type", nullable = false)
    private String aggregateType;

    @Column(name = "event_type", nullable = false)
    private String eventType;

    @Column(name = "payload", columnDefinition = "text", nullable = false)
    private String payload;

    @Column(name = "created_at", nullable = false)
    private Instant createdAt;

    @Column(name = "failed_at", nullable = false)
    private Instant failedAt;

    @Column(name = "attempts", nullable = false)
    private int attempts;

    @Column(name = "last_error", columnDefinition = "text")
    private String lastError;

    public OutboxDeadLetterEntity() {}

    public static OutboxDeadLetterEntity from(OutboxEventEntity event, String error) {
        OutboxDeadLetterEntity dead = new OutboxDeadLetterEntity();
        dead.id = event.getId();
        dead.aggregateId = event.getAggregateId();
        dead.aggregateType = event.getAggregateType();
        dead.eventType = event.getEventType();
        dead.payload = event.getPayload();
        dead.createdAt = event.getCreatedAt();
        dead.failedAt = Instant.now();
        dead.attempts = event.getAttempts();
        dead.lastError = error;
        return dead;
    }

    public UUID getId() {
        return id;
    }

    public String getAggregateId() {
        return aggregateId;
    }

    public String getAggregateType() {
        return aggregateType;
    }

    public String getEventType() {
        return eventType;
    }

    public String getPayload() {
        return payload;
    }

    public Instant getCreatedAt() {
        return createdAt;
    }

    public Instant getFailedAt() {
        return failedAt;
    }

    public int getAttempts() {
        return attempts;
    }

    public String getLastError() {
        return lastError;
    }
}
