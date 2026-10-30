package com.bmscomp.kates.persistence;

import java.time.Instant;
import java.time.temporal.ChronoUnit;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.GeneratedValue;
import jakarta.persistence.GenerationType;
import jakarta.persistence.Id;
import jakarta.persistence.Table;

@Entity
@Table(name = "audit_events")
public class AuditEventEntity {

    @Id
    @GeneratedValue(strategy = GenerationType.IDENTITY)
    private Long id;

    @Column(nullable = false, length = 32)
    private String action;

    @Column(name = "event_type", nullable = false, length = 32)
    private String eventType;

    @Column(length = 128)
    private String target;

    @Column(columnDefinition = "TEXT")
    private String details;

    @Column(name = "created_at", nullable = false)
    private Instant createdAt;

    /** The principal that acted (V26); null on older rows. */
    @Column(length = 64)
    private String actor;

    /** human, agent or system (V26); null on older rows. */
    @Column(name = "principal_type", length = 16)
    private String principalType;

    /** The row_hash of the row before this one in the chain (V28); null on older rows. */
    @Column(name = "prev_hash", length = 64)
    private String prevHash;

    /** SHA-256 over prev_hash and this row's fields (V28); null on older rows. */
    @Column(name = "row_hash", length = 64)
    private String rowHash;

    public AuditEventEntity() {}

    public AuditEventEntity(String action, String eventType, String target, String details) {
        this.action = action;
        this.eventType = eventType;
        this.target = target;
        this.details = details;
        // The database keeps microseconds, so the hash covers what is read back.
        this.createdAt = Instant.now().truncatedTo(ChronoUnit.MICROS);
    }

    public Long getId() {
        return id;
    }

    public void setId(Long id) {
        this.id = id;
    }

    public String getAction() {
        return action;
    }

    public void setAction(String action) {
        this.action = action;
    }

    public String getEventType() {
        return eventType;
    }

    public void setEventType(String eventType) {
        this.eventType = eventType;
    }

    public String getTarget() {
        return target;
    }

    public void setTarget(String target) {
        this.target = target;
    }

    public String getDetails() {
        return details;
    }

    public void setDetails(String details) {
        this.details = details;
    }

    public Instant getCreatedAt() {
        return createdAt;
    }

    public void setCreatedAt(Instant createdAt) {
        this.createdAt = createdAt;
    }

    public String getActor() {
        return actor;
    }

    public void setActor(String actor) {
        this.actor = actor;
    }

    public String getPrincipalType() {
        return principalType;
    }

    public void setPrincipalType(String principalType) {
        this.principalType = principalType;
    }

    public String getPrevHash() {
        return prevHash;
    }

    public void setPrevHash(String prevHash) {
        this.prevHash = prevHash;
    }

    public String getRowHash() {
        return rowHash;
    }

    public void setRowHash(String rowHash) {
        this.rowHash = rowHash;
    }
}
