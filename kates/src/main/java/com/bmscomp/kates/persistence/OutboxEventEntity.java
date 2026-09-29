package com.bmscomp.kates.persistence;

import java.time.Instant;
import java.util.UUID;
import jakarta.persistence.*;

@Entity
@Table(name = "outbox_events")
public class OutboxEventEntity {

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

    /**
     * Failed publish attempts. A row that can never be published (unparseable
     * payload, permanently rejected message) would otherwise be retried on every
     * poll forever while holding a slot at the head of the poll window.
     */
    @Column(name = "attempts", nullable = false)
    private int attempts;

    @Column(name = "last_error", columnDefinition = "text")
    private String lastError;

    public OutboxEventEntity() {}

    public OutboxEventEntity(String aggregateId, String aggregateType, String eventType, String payload) {
        this.id = UUID.randomUUID();
        this.aggregateId = aggregateId;
        this.aggregateType = aggregateType;
        this.eventType = eventType;
        this.payload = payload;
        this.createdAt = Instant.now();
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

    public int getAttempts() {
        return attempts;
    }

    public void setAttempts(int attempts) {
        this.attempts = attempts;
    }

    public String getLastError() {
        return lastError;
    }

    public void setLastError(String lastError) {
        this.lastError = lastError;
    }
}
