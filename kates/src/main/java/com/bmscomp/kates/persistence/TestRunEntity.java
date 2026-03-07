package com.bmscomp.kates.persistence;

import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import jakarta.persistence.CascadeType;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.FetchType;
import jakarta.persistence.Id;
import jakarta.persistence.OneToMany;
import jakarta.persistence.OrderBy;
import jakarta.persistence.Table;

import org.hibernate.annotations.JdbcTypeCode;
import org.hibernate.type.SqlTypes;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestType;

@Entity
@Table(name = "test_runs")
@jakarta.persistence.Cacheable
public class TestRunEntity {

    @Id
    @Column(length = 36)
    private String id;

    @Enumerated(EnumType.STRING)
    @Column(name = "test_type", length = 32)
    private TestType testType;

    @Enumerated(EnumType.STRING)
    @Column(length = 16)
    private TestResult.TaskStatus status;

    @Column(name = "created_at")
    private Instant createdAt;

    @Column(length = 32)
    private String backend;

    @Column(name = "scenario_name", length = 128)
    private String scenarioName;

    @Column(name = "spec_json", columnDefinition = "TEXT")
    private String specJson;

    @Column(name = "sla_json", columnDefinition = "TEXT")
    private String slaJson;

    @JdbcTypeCode(SqlTypes.JSON)
    @Column(name = "labels_json", columnDefinition = "jsonb")
    private String labelsJson;

    @OneToMany(mappedBy = "testRun", cascade = CascadeType.ALL, orphanRemoval = true, fetch = FetchType.EAGER)
    @OrderBy("id ASC")
    private List<TestResultEntity> results = new ArrayList<>();

    public TestRunEntity() {}

    public String getId() {
        return id;
    }

    public void setId(String id) {
        this.id = id;
    }

    public TestType getTestType() {
        return testType;
    }

    public void setTestType(TestType testType) {
        this.testType = testType;
    }

    public TestResult.TaskStatus getStatus() {
        return status;
    }

    public void setStatus(TestResult.TaskStatus status) {
        this.status = status;
    }

    public Instant getCreatedAt() {
        return createdAt;
    }

    public void setCreatedAt(Instant createdAt) {
        this.createdAt = createdAt;
    }

    public String getBackend() {
        return backend;
    }

    public void setBackend(String backend) {
        this.backend = backend;
    }

    public String getScenarioName() {
        return scenarioName;
    }

    public void setScenarioName(String scenarioName) {
        this.scenarioName = scenarioName;
    }

    public String getSpecJson() {
        return specJson;
    }

    public void setSpecJson(String specJson) {
        this.specJson = specJson;
    }

    public String getSlaJson() {
        return slaJson;
    }

    public void setSlaJson(String slaJson) {
        this.slaJson = slaJson;
    }

    public String getLabelsJson() {
        return labelsJson;
    }

    public void setLabelsJson(String labelsJson) {
        this.labelsJson = labelsJson;
    }

    public List<TestResultEntity> getResults() {
        return results;
    }

    public void setResults(List<TestResultEntity> results) {
        this.results = results;
    }

    public void addResult(TestResultEntity result) {
        results.add(result);
        result.setTestRun(this);
    }
}
