package com.klster.kates.resilience;

import com.klster.kates.chaos.FaultSpec;
import com.klster.kates.domain.CreateTestRequest;

/**
 * Request combining a performance test with a chaos fault injection.
 * The benchmark runs first, waits for a steady-state period, then injects the fault.
 */
public class ResilienceTestRequest {

    private CreateTestRequest testRequest;
    private FaultSpec chaosSpec;
    private int steadyStateSec = 30;

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
}
