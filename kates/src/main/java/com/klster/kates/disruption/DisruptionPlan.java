package com.klster.kates.disruption;

import java.util.ArrayList;
import java.util.List;

import com.klster.kates.chaos.FaultSpec;
import com.klster.kates.domain.SlaDefinition;

/**
 * Multi-step disruption plan. Each step defines a fault to inject,
 * observation windows, and recovery requirements.
 */
public class DisruptionPlan {

    private String name;
    private String description;
    private List<DisruptionStep> steps = new ArrayList<>();
    private SlaDefinition sla;
    private String testType;
    private int baselineDurationSec = 60;
    private String isrTrackingTopic;
    private String lagTrackingGroupId;
    private int isrPollIntervalMs = 2000;
    private int lagPollIntervalMs = 2000;
    private int maxAffectedBrokers = -1;
    private boolean autoRollback = true;

    public DisruptionPlan() {}

    public record DisruptionStep(
            String name, FaultSpec faultSpec, int steadyStateSec, int observationWindowSec, boolean requireRecovery) {}

    public String getName() {
        return name;
    }

    public void setName(String name) {
        this.name = name;
    }

    public String getDescription() {
        return description;
    }

    public void setDescription(String description) {
        this.description = description;
    }

    public List<DisruptionStep> getSteps() {
        return steps;
    }

    public void setSteps(List<DisruptionStep> steps) {
        this.steps = steps;
    }

    public SlaDefinition getSla() {
        return sla;
    }

    public void setSla(SlaDefinition sla) {
        this.sla = sla;
    }

    public String getTestType() {
        return testType;
    }

    public void setTestType(String testType) {
        this.testType = testType;
    }

    public int getBaselineDurationSec() {
        return baselineDurationSec;
    }

    public void setBaselineDurationSec(int baselineDurationSec) {
        this.baselineDurationSec = baselineDurationSec;
    }

    public String getIsrTrackingTopic() {
        return isrTrackingTopic;
    }

    public void setIsrTrackingTopic(String isrTrackingTopic) {
        this.isrTrackingTopic = isrTrackingTopic;
    }

    public String getLagTrackingGroupId() {
        return lagTrackingGroupId;
    }

    public void setLagTrackingGroupId(String lagTrackingGroupId) {
        this.lagTrackingGroupId = lagTrackingGroupId;
    }

    public int getIsrPollIntervalMs() {
        return isrPollIntervalMs;
    }

    public void setIsrPollIntervalMs(int isrPollIntervalMs) {
        this.isrPollIntervalMs = isrPollIntervalMs;
    }

    public int getLagPollIntervalMs() {
        return lagPollIntervalMs;
    }

    public void setLagPollIntervalMs(int lagPollIntervalMs) {
        this.lagPollIntervalMs = lagPollIntervalMs;
    }

    public int getMaxAffectedBrokers() {
        return maxAffectedBrokers;
    }

    public void setMaxAffectedBrokers(int maxAffectedBrokers) {
        this.maxAffectedBrokers = maxAffectedBrokers;
    }

    public boolean isAutoRollback() {
        return autoRollback;
    }

    public void setAutoRollback(boolean autoRollback) {
        this.autoRollback = autoRollback;
    }
}
