package com.bmscomp.kates.disruption;

import java.util.List;

/**
 * Request/response DTOs for the disruption API.
 */
public final class DisruptionDtos {

    private DisruptionDtos() {}

    public static class CompoundChaosRequest {
        public List<CompoundFaultEntry> faults;
        public boolean sequential = false;
        public int timeoutSec = 120;
        public int delayBetweenSec = 5;
    }

    public static class CompoundFaultEntry {
        public com.bmscomp.kates.chaos.FaultSpec faultSpec;
        public String providerName;
    }

    public static class CreateDisruptionScheduleRequest {
        public String name;
        public String cronExpression;
        public boolean enabled = true;
        public String playbookName;
        public DisruptionPlan plan;
    }
}
