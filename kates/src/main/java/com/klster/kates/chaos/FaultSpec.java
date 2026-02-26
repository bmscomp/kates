package com.klster.kates.chaos;

import java.util.Map;

/**
 * Immutable descriptor for a fault injection experiment.
 * Backend-agnostic — each {@link ChaosProvider} maps this to its native format.
 *
 * <p>For Kubernetes-aware disruptions, set {@code disruptionType} and the
 * corresponding parameters (targetBrokerId, networkLatencyMs, etc.).
 * Legacy callers using only {@code experimentName} continue to work unchanged.
 */
public record FaultSpec(
        String experimentName,
        String targetNamespace,
        String targetLabel,
        String targetPod,
        int chaosDurationSec,
        int delayBeforeSec,
        Map<String, String> envOverrides,
        DisruptionType disruptionType,
        int targetBrokerId,
        int networkLatencyMs,
        int fillPercentage,
        int cpuCores,
        int gracePeriodSec,
        String targetTopic,
        int targetPartition,
        java.util.List<ProbeSpec> probes) {
    public static Builder builder(String experimentName) {
        return new Builder(experimentName);
    }

    public static class Builder {
        private final String experimentName;
        private String targetNamespace = "kafka";
        private String targetLabel = "strimzi.io/component-type=kafka";
        private String targetPod = "";
        private int chaosDurationSec = 30;
        private int delayBeforeSec = 0;
        private Map<String, String> envOverrides = Map.of();
        private DisruptionType disruptionType;
        private int targetBrokerId = -1;
        private int networkLatencyMs = 100;
        private int fillPercentage = 80;
        private int cpuCores = 1;
        private int gracePeriodSec = 30;
        private String targetTopic = "";
        private int targetPartition = 0;
        private java.util.List<ProbeSpec> probes = java.util.List.of();

        private Builder(String experimentName) {
            this.experimentName = experimentName;
        }

        public Builder targetNamespace(String v) {
            this.targetNamespace = v;
            return this;
        }

        public Builder targetLabel(String v) {
            this.targetLabel = v;
            return this;
        }

        public Builder targetPod(String v) {
            this.targetPod = v;
            return this;
        }

        public Builder chaosDurationSec(int v) {
            this.chaosDurationSec = v;
            return this;
        }

        public Builder delayBeforeSec(int v) {
            this.delayBeforeSec = v;
            return this;
        }

        public Builder envOverrides(Map<String, String> v) {
            this.envOverrides = v;
            return this;
        }

        public Builder disruptionType(DisruptionType v) {
            this.disruptionType = v;
            return this;
        }

        public Builder targetBrokerId(int v) {
            this.targetBrokerId = v;
            return this;
        }

        public Builder networkLatencyMs(int v) {
            this.networkLatencyMs = v;
            return this;
        }

        public Builder fillPercentage(int v) {
            this.fillPercentage = v;
            return this;
        }

        public Builder cpuCores(int v) {
            this.cpuCores = v;
            return this;
        }

        public Builder gracePeriodSec(int v) {
            this.gracePeriodSec = v;
            return this;
        }

        public Builder targetTopic(String v) {
            this.targetTopic = v;
            return this;
        }

        public Builder targetPartition(int v) {
            this.targetPartition = v;
            return this;
        }

        public Builder probes(java.util.List<ProbeSpec> v) {
            this.probes = v;
            return this;
        }

        public FaultSpec build() {
            return new FaultSpec(
                    experimentName,
                    targetNamespace,
                    targetLabel,
                    targetPod,
                    chaosDurationSec,
                    delayBeforeSec,
                    Map.copyOf(envOverrides),
                    disruptionType,
                    targetBrokerId,
                    networkLatencyMs,
                    fillPercentage,
                    cpuCores,
                    gracePeriodSec,
                    targetTopic,
                    targetPartition,
                    java.util.List.copyOf(probes));
        }
    }
}
