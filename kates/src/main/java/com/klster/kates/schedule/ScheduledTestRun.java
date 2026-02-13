package com.klster.kates.schedule;

import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.Id;
import jakarta.persistence.Table;
import java.time.Instant;

/**
 * JPA entity for a scheduled/recurring test configuration.
 * Stores the cron expression and the serialized CreateTestRequest.
 */
@Entity
@Table(name = "scheduled_test_runs")
public class ScheduledTestRun {

    @Id
    @Column(length = 36)
    private String id;

    @Column(length = 128, nullable = false)
    private String name;

    @Column(name = "cron_expression", length = 64, nullable = false)
    private String cronExpression;

    @Column(nullable = false)
    private boolean enabled = true;

    @Column(name = "request_json", columnDefinition = "TEXT", nullable = false)
    private String requestJson;

    @Column(name = "last_run_id", length = 36)
    private String lastRunId;

    @Column(name = "last_run_at")
    private Instant lastRunAt;

    @Column(name = "created_at", nullable = false)
    private Instant createdAt;

    public ScheduledTestRun() {
        this.createdAt = Instant.now();
    }

    public String getId() { return id; }
    public void setId(String id) { this.id = id; }

    public String getName() { return name; }
    public void setName(String name) { this.name = name; }

    public String getCronExpression() { return cronExpression; }
    public void setCronExpression(String cronExpression) { this.cronExpression = cronExpression; }

    public boolean isEnabled() { return enabled; }
    public void setEnabled(boolean enabled) { this.enabled = enabled; }

    public String getRequestJson() { return requestJson; }
    public void setRequestJson(String requestJson) { this.requestJson = requestJson; }

    public String getLastRunId() { return lastRunId; }
    public void setLastRunId(String lastRunId) { this.lastRunId = lastRunId; }

    public Instant getLastRunAt() { return lastRunAt; }
    public void setLastRunAt(Instant lastRunAt) { this.lastRunAt = lastRunAt; }

    public Instant getCreatedAt() { return createdAt; }
    public void setCreatedAt(Instant createdAt) { this.createdAt = createdAt; }
}
