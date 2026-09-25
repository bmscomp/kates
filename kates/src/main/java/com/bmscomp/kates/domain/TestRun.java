package com.bmscomp.kates.domain;

import java.time.Instant;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;

import com.fasterxml.jackson.annotation.JsonInclude;

@JsonInclude(JsonInclude.Include.NON_NULL)
public class TestRun {

    private final String id;
    private final TestType testType;
    private final TestSpec spec;
    private final TestResult.TaskStatus status;
    private final List<TestResult> results;
    private final String createdAt;
    private final String backend;
    private final String scenarioName;
    private final Map<String, String> labels;
    private final SlaDefinition sla;
    private final Map<String, Long> cdcPhases;
    private final String cdcPhase;
    /**
     * The spec as the request sent it: only the fields it set (see
     * {@link TestSpec#explicitFields()}). {@link #spec} is what the run used,
     * after the type defaults filled the rest. Null for runs stored before the
     * request was kept.
     */
    private final Map<String, Object> requestedSpec;

    public TestRun() {
        this(
                UUID.randomUUID().toString().substring(0, 8),
                null,
                null,
                TestResult.TaskStatus.PENDING,
                new ArrayList<>(),
                Instant.now().toString(),
                null,
                null,
                new LinkedHashMap<>(),
                null,
                null,
                null,
                null);
    }

    public TestRun(TestType testType, TestSpec spec) {
        this(
                UUID.randomUUID().toString().substring(0, 8),
                testType,
                spec,
                TestResult.TaskStatus.PENDING,
                new ArrayList<>(),
                Instant.now().toString(),
                null,
                null,
                new LinkedHashMap<>(),
                null,
                null,
                null,
                null);
    }

    private TestRun(
            String id,
            TestType testType,
            TestSpec spec,
            TestResult.TaskStatus status,
            List<TestResult> results,
            String createdAt,
            String backend,
            String scenarioName,
            Map<String, String> labels,
            SlaDefinition sla,
            Map<String, Long> cdcPhases,
            String cdcPhase,
            Map<String, Object> requestedSpec) {
        this.id = id;
        this.testType = testType;
        this.spec = spec;
        this.status = status;
        this.results = new ArrayList<>(results != null ? results : List.of());
        this.createdAt = createdAt;
        this.backend = backend;
        this.scenarioName = scenarioName;
        this.labels = new LinkedHashMap<>(labels != null ? labels : Map.of());
        this.sla = sla;
        this.cdcPhases = cdcPhases != null ? new LinkedHashMap<>(cdcPhases) : null;
        this.cdcPhase = cdcPhase;
        this.requestedSpec = requestedSpec != null ? new LinkedHashMap<>(requestedSpec) : null;
    }

    public TestRun withResult(TestResult result) {
        List<TestResult> newResults = new ArrayList<>(this.results);
        newResults.add(result);
        return new TestRun(
                id,
                testType,
                spec,
                status,
                newResults,
                createdAt,
                backend,
                scenarioName,
                labels,
                sla,
                cdcPhases,
                cdcPhase,
                requestedSpec);
    }

    public TestRun withId(String id) {
        return new TestRun(
                id,
                testType,
                spec,
                status,
                results,
                createdAt,
                backend,
                scenarioName,
                labels,
                sla,
                cdcPhases,
                cdcPhase,
                requestedSpec);
    }

    public TestRun withTestType(TestType testType) {
        return new TestRun(
                id,
                testType,
                spec,
                status,
                results,
                createdAt,
                backend,
                scenarioName,
                labels,
                sla,
                cdcPhases,
                cdcPhase,
                requestedSpec);
    }

    public TestRun withSpec(TestSpec spec) {
        return new TestRun(
                id,
                testType,
                spec,
                status,
                results,
                createdAt,
                backend,
                scenarioName,
                labels,
                sla,
                cdcPhases,
                cdcPhase,
                requestedSpec);
    }

    public TestRun withStatus(TestResult.TaskStatus status) {
        return new TestRun(
                id,
                testType,
                spec,
                status,
                results,
                createdAt,
                backend,
                scenarioName,
                labels,
                sla,
                cdcPhases,
                cdcPhase,
                requestedSpec);
    }

    public TestRun withResults(List<TestResult> results) {
        return new TestRun(
                id,
                testType,
                spec,
                status,
                results,
                createdAt,
                backend,
                scenarioName,
                labels,
                sla,
                cdcPhases,
                cdcPhase,
                requestedSpec);
    }

    public TestRun withCreatedAt(String createdAt) {
        return new TestRun(
                id,
                testType,
                spec,
                status,
                results,
                createdAt,
                backend,
                scenarioName,
                labels,
                sla,
                cdcPhases,
                cdcPhase,
                requestedSpec);
    }

    public TestRun withBackend(String backend) {
        return new TestRun(
                id,
                testType,
                spec,
                status,
                results,
                createdAt,
                backend,
                scenarioName,
                labels,
                sla,
                cdcPhases,
                cdcPhase,
                requestedSpec);
    }

    public TestRun withScenarioName(String scenarioName) {
        return new TestRun(
                id,
                testType,
                spec,
                status,
                results,
                createdAt,
                backend,
                scenarioName,
                labels,
                sla,
                cdcPhases,
                cdcPhase,
                requestedSpec);
    }

    public TestRun withLabels(Map<String, String> labels) {
        return new TestRun(
                id,
                testType,
                spec,
                status,
                results,
                createdAt,
                backend,
                scenarioName,
                labels,
                sla,
                cdcPhases,
                cdcPhase,
                requestedSpec);
    }

    public TestRun withSla(SlaDefinition sla) {
        return new TestRun(
                id,
                testType,
                spec,
                status,
                results,
                createdAt,
                backend,
                scenarioName,
                labels,
                sla,
                cdcPhases,
                cdcPhase,
                requestedSpec);
    }

    public TestRun withCdcPhases(Map<String, Long> cdcPhases) {
        return new TestRun(
                id,
                testType,
                spec,
                status,
                results,
                createdAt,
                backend,
                scenarioName,
                labels,
                sla,
                cdcPhases,
                cdcPhase,
                requestedSpec);
    }

    public TestRun withCdcPhase(String cdcPhase) {
        return new TestRun(
                id,
                testType,
                spec,
                status,
                results,
                createdAt,
                backend,
                scenarioName,
                labels,
                sla,
                cdcPhases,
                cdcPhase,
                requestedSpec);
    }

    public TestRun withRequestedSpec(Map<String, Object> requestedSpec) {
        return new TestRun(
                id,
                testType,
                spec,
                status,
                results,
                createdAt,
                backend,
                scenarioName,
                labels,
                sla,
                cdcPhases,
                cdcPhase,
                requestedSpec);
    }

    public TestRun withAddedResult(TestResult result) {
        List<TestResult> newResults =
                new java.util.ArrayList<>(this.results != null ? this.results : java.util.Collections.emptyList());
        newResults.add(result);
        return new TestRun(
                id,
                testType,
                spec,
                status,
                newResults,
                createdAt,
                backend,
                scenarioName,
                labels,
                sla,
                cdcPhases,
                cdcPhase,
                requestedSpec);
    }

    public TestRun withUpdatedResult(TestResult updatedResult) {
        if (this.results == null) return this;
        List<TestResult> newResults = this.results.stream()
                .map(r -> r.getTaskId().equals(updatedResult.getTaskId()) ? updatedResult : r)
                .collect(java.util.stream.Collectors.toList());
        return new TestRun(
                id,
                testType,
                spec,
                status,
                newResults,
                createdAt,
                backend,
                scenarioName,
                labels,
                sla,
                cdcPhases,
                cdcPhase,
                requestedSpec);
    }

    public String getId() {
        return id;
    }

    public TestType getTestType() {
        return testType;
    }

    public TestSpec getSpec() {
        return spec;
    }

    public TestResult.TaskStatus getStatus() {
        return status;
    }

    public List<TestResult> getResults() {
        return results;
    }

    public String getCreatedAt() {
        return createdAt;
    }

    public String getBackend() {
        return backend;
    }

    public String getScenarioName() {
        return scenarioName;
    }

    public Map<String, String> getLabels() {
        return labels;
    }

    public SlaDefinition getSla() {
        return sla;
    }

    public Map<String, Long> getCdcPhases() {
        return cdcPhases;
    }

    public String getCdcPhase() {
        return cdcPhase;
    }

    public Map<String, Object> getRequestedSpec() {
        return requestedSpec;
    }
}
