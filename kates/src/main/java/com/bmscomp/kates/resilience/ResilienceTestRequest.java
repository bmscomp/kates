package com.bmscomp.kates.resilience;

import java.util.List;

import com.bmscomp.kates.chaos.FaultSpec;
import com.bmscomp.kates.chaos.ProbeSpec;
import com.bmscomp.kates.domain.CreateTestRequest;

/**
 * Request combining a performance test with a chaos fault injection.
 * The benchmark runs first, waits for a steady-state period, then injects the fault.
 * Probes are evaluated at baseline, during chaos, and post-recovery.
 */
public class ResilienceTestRequest {

    private CreateTestRequest testRequest;
    private FaultSpec chaosSpec;
    private int steadyStateSec = 30;
    private List<ProbeSpec> probes;
    private int maxRecoveryWaitSec = 120;

    public CreateTestRequest getTestRequest() {
        return testRequest;
    }

    public void setTestRequest(CreateTestRequest testRequest) {
        this.testRequest = testRequest;
    }

    public FaultSpec getChaosSpec() {
        return chaosSpec;
    }

    public void setChaosSpec(FaultSpec chaosSpec) {
        this.chaosSpec = chaosSpec;
    }

    public int getSteadyStateSec() {
        return steadyStateSec;
    }

    public void setSteadyStateSec(int steadyStateSec) {
        this.steadyStateSec = steadyStateSec;
    }

    public List<ProbeSpec> getProbes() {
        return probes;
    }

    public void setProbes(List<ProbeSpec> probes) {
        this.probes = probes;
    }

    public int getMaxRecoveryWaitSec() {
        return maxRecoveryWaitSec;
    }

    public void setMaxRecoveryWaitSec(int maxRecoveryWaitSec) {
        this.maxRecoveryWaitSec = maxRecoveryWaitSec;
    }
}
