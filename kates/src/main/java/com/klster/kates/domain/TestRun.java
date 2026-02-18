package com.klster.kates.domain;

import java.time.Instant;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;

import com.fasterxml.jackson.annotation.JsonInclude;

@JsonInclude(JsonInclude.Include.NON_NULL)
public class TestRun {

    private String id;
    private TestType testType;
    private TestSpec spec;
    private TestResult.TaskStatus status;
    private List<TestResult> results;
    private String createdAt;
    private String backend;
    private String scenarioName;
    private Map<String, String> labels = new LinkedHashMap<>();
    private SlaDefinition sla;

    public TestRun() {
        this.id = UUID.randomUUID().toString().substring(0, 8);
        this.status = TestResult.TaskStatus.PENDING;
        this.results = new ArrayList<>();
        this.createdAt = Instant.now().toString();
    }

    public TestRun(TestType testType, TestSpec spec) {
        this();
        this.testType = testType;
        this.spec = spec;
    }

    public void addResult(TestResult result) {
        this.results.add(result);
    }

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

    public TestSpec getSpec() {
        return spec;
    }

    public void setSpec(TestSpec spec) {
        this.spec = spec;
    }

    public TestResult.TaskStatus getStatus() {
        return status;
    }

    public void setStatus(TestResult.TaskStatus status) {
        this.status = status;
    }

    public List<TestResult> getResults() {
        return results;
    }

    public void setResults(List<TestResult> results) {
        this.results = results;
    }

    public String getCreatedAt() {
        return createdAt;
    }

    public void setCreatedAt(String createdAt) {
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

    public Map<String, String> getLabels() {
        return labels;
    }

    public void setLabels(Map<String, String> labels) {
        this.labels = labels;
    }

    public SlaDefinition getSla() {
        return sla;
    }

    public void setSla(SlaDefinition sla) {
        this.sla = sla;
    }
}
