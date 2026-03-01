package com.bmscomp.kates.disruption;

import java.time.Instant;
import jakarta.persistence.*;

/**
 * JPA entity for a scheduled/recurring disruption test.
 * Supports either a playbook name or a raw plan JSON.
 */
@Entity
@Table(name = "disruption_schedules")
public class DisruptionScheduleEntity {

    @Id
    @Column(length = 36)
    private String id;

    @Column(nullable = false, length = 128)
    private String name;

    @Column(name = "cron_expression", nullable = false, length = 64)
    private String cronExpression;

    @Column(nullable = false)
    private boolean enabled = true;

    @Column(name = "playbook_name", length = 64)
    private String playbookName;

    @Column(name = "plan_json", columnDefinition = "TEXT")
    private String planJson;

    @Column(name = "last_run_id", length = 36)
    private String lastRunId;

    @Column(name = "last_run_at")
    private Instant lastRunAt;

    @Column(name = "created_at", nullable = false)
    private Instant createdAt;

    public DisruptionScheduleEntity() {
        this.createdAt = Instant.now();
    }

    public String getId() {
        return id;
    }

    public void setId(String id) {
        this.id = id;
    }

    public String getName() {
        return name;
    }

    public void setName(String name) {
        this.name = name;
    }

    public String getCronExpression() {
        return cronExpression;
    }

    public void setCronExpression(String cronExpression) {
        this.cronExpression = cronExpression;
    }

    public boolean isEnabled() {
        return enabled;
    }

    public void setEnabled(boolean enabled) {
        this.enabled = enabled;
    }

    public String getPlaybookName() {
        return playbookName;
    }

    public void setPlaybookName(String playbookName) {
        this.playbookName = playbookName;
    }

    public String getPlanJson() {
        return planJson;
    }

    public void setPlanJson(String planJson) {
        this.planJson = planJson;
    }

    public String getLastRunId() {
        return lastRunId;
    }

    public void setLastRunId(String lastRunId) {
        this.lastRunId = lastRunId;
    }

    public Instant getLastRunAt() {
        return lastRunAt;
    }

    public void setLastRunAt(Instant lastRunAt) {
        this.lastRunAt = lastRunAt;
    }

    public Instant getCreatedAt() {
        return createdAt;
    }

    public void setCreatedAt(Instant createdAt) {
        this.createdAt = createdAt;
    }
}
