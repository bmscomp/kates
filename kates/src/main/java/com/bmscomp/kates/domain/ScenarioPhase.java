package com.bmscomp.kates.domain;

import com.fasterxml.jackson.annotation.JsonInclude;

/**
 * Defines a single phase within a {@link TestScenario}.
 * Each phase represents one workload step (warmup, ramp, steady, spike, cooldown)
 * with its own duration, target throughput, and optional spec overrides.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public class ScenarioPhase {

    public enum PhaseType {
        WARMUP,
        RAMP,
        STEADY,
        SPIKE,
        COOLDOWN
    }

    private String name;
    private PhaseType phaseType;
    private TestSpec spec;
    private long durationMs;
    private int targetThroughput = -1;
    private int rampSteps = 1;

    public ScenarioPhase() {}

    public ScenarioPhase(String name, PhaseType phaseType, long durationMs, int targetThroughput) {
        this.name = name;
        this.phaseType = phaseType;
        this.durationMs = durationMs;
        this.targetThroughput = targetThroughput;
    }

    public String getName() {
        return name;
    }

    public void setName(String name) {
        this.name = name;
    }

    public PhaseType getPhaseType() {
        return phaseType;
    }

    public void setPhaseType(PhaseType phaseType) {
        this.phaseType = phaseType;
    }

    public TestSpec getSpec() {
        return spec;
    }

    public void setSpec(TestSpec spec) {
        this.spec = spec;
    }

    public long getDurationMs() {
        return durationMs;
    }

    public void setDurationMs(long durationMs) {
        this.durationMs = durationMs;
    }

    public int getTargetThroughput() {
        return targetThroughput;
    }

    public void setTargetThroughput(int targetThroughput) {
        this.targetThroughput = targetThroughput;
    }

    public int getRampSteps() {
        return rampSteps;
    }

    public void setRampSteps(int rampSteps) {
        this.rampSteps = rampSteps;
    }
}
