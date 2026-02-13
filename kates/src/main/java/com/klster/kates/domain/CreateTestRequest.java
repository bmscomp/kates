package com.klster.kates.domain;

import com.fasterxml.jackson.annotation.JsonInclude;

@JsonInclude(JsonInclude.Include.NON_NULL)
public class CreateTestRequest {

    private TestType type;
    private TestSpec spec;
    private String backend;
    private TestScenario scenario;

    public CreateTestRequest() {
    }

    public TestType getType() {
        return type;
    }

    public void setType(TestType type) {
        this.type = type;
    }

    public TestSpec getSpec() {
        return spec;
    }

    public void setSpec(TestSpec spec) {
        this.spec = spec;
    }

    public String getBackend() {
        return backend;
    }

    public void setBackend(String backend) {
        this.backend = backend;
    }

    public TestScenario getScenario() {
        return scenario;
    }

    public void setScenario(TestScenario scenario) {
        this.scenario = scenario;
    }

    public boolean isScenario() {
        return scenario != null && scenario.getPhases() != null && !scenario.getPhases().isEmpty();
    }
}
