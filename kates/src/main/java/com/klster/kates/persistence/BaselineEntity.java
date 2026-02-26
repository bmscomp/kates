package com.klster.kates.persistence;

import java.time.Instant;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.Id;
import jakarta.persistence.Table;

import com.klster.kates.domain.TestType;

@Entity
@Table(name = "baseline_runs")
public class BaselineEntity {

    @Id
    @Enumerated(EnumType.STRING)
    @Column(name = "test_type", length = 32)
    private TestType testType;

    @Column(name = "run_id", length = 36, nullable = false)
    private String runId;

    @Column(name = "set_at", nullable = false)
    private Instant setAt;

    public BaselineEntity() {}

    public BaselineEntity(TestType testType, String runId) {
        this.testType = testType;
        this.runId = runId;
        this.setAt = Instant.now();
    }

    public TestType getTestType() { return testType; }
    public void setTestType(TestType testType) { this.testType = testType; }
    public String getRunId() { return runId; }
    public void setRunId(String runId) { this.runId = runId; }
    public Instant getSetAt() { return setAt; }
    public void setSetAt(Instant setAt) { this.setAt = setAt; }
}
