package com.bmscomp.kates.chaos;

import java.util.Map;

import com.fasterxml.jackson.annotation.JsonCreator;
import com.fasterxml.jackson.annotation.JsonProperty;
import com.fasterxml.jackson.databind.annotation.JsonDeserialize;
import com.fasterxml.jackson.databind.annotation.JsonPOJOBuilder;

/**
 * Immutable descriptor for a fault injection experiment.
 * Backend-agnostic — each {@link ChaosProvider} maps this to its native format.
 *
 * <p>For Kubernetes-aware disruptions, set {@code disruptionType} and the
 * corresponding parameters (targetBrokerId, networkLatencyMs, etc.).
 * Legacy callers using only {@code experimentName} continue to work unchanged.
 *
 * <p>{@code targetLabel} is a Kubernetes label selector ({@link ParsedLabelSelector}).
 * A pod-scoped fault hits one pod it matches unless {@code targetAll} is set;
 * {@link PodTargets} has the full precedence.
 *
 * <p>JSON is read through the {@link Builder}, so a field the JSON leaves out
 * gets the builder's default. Read through the canonical constructor, as
 * Jackson does for a plain record, it was {@code null}, {@code 0} or
 * {@code false}: no namespace or label selector, broker 0 instead of a random
 * pod, and a zero duration and grace period.
 */
@JsonDeserialize(builder = FaultSpec.Builder.class)
public record FaultSpec(
        String experimentName,
        String targetNamespace,
        String targetLabel,
        String targetPod,
        boolean targetAll,
        int chaosDurationSec,
        int delayBeforeSec,
        Map<String, String> envOverrides,
        DisruptionType disruptionType,
        int targetBrokerId,
        int networkLatencyMs,
        int fillPercentage,
        int cpuCores,
        int memoryMb,
        int ioWorkers,
        int gracePeriodSec,
        String targetTopic,
        int targetPartition,
        java.util.List<ProbeSpec> probes) {
    public static Builder builder(String experimentName) {
        return new Builder(experimentName);
    }

    /**
     * A builder holding every component of this spec, for a copy that changes
     * a few of them. A spec built with the canonical constructor may hold null
     * collections; the copy has empty ones.
     */
    public Builder toBuilder() {
        return new Builder(experimentName)
                .targetNamespace(targetNamespace)
                .targetLabel(targetLabel)
                .targetPod(targetPod)
                .targetAll(targetAll)
                .chaosDurationSec(chaosDurationSec)
                .delayBeforeSec(delayBeforeSec)
                .envOverrides(envOverrides)
                .disruptionType(disruptionType)
                .targetBrokerId(targetBrokerId)
                .networkLatencyMs(networkLatencyMs)
                .fillPercentage(fillPercentage)
                .cpuCores(cpuCores)
                .memoryMb(memoryMb)
                .ioWorkers(ioWorkers)
                .gracePeriodSec(gracePeriodSec)
                .targetTopic(targetTopic)
                .targetPartition(targetPartition)
                .probes(probes);
    }

    @JsonPOJOBuilder(withPrefix = "")
    public static class Builder {
        private final String experimentName;
        private String targetNamespace = "kafka";
        private String targetLabel = "strimzi.io/component-type=kafka";
        private String targetPod = "";
        private boolean targetAll = false;
        private int chaosDurationSec = 30;
        private int delayBeforeSec = 0;
        private Map<String, String> envOverrides = Map.of();
        private DisruptionType disruptionType;
        private int targetBrokerId = -1;
        private int networkLatencyMs = 100;
        private int fillPercentage = 80;
        private int cpuCores = 1;
        private int memoryMb = 500;
        private int ioWorkers = 2;
        private int gracePeriodSec = 30;
        private String targetTopic = "";
        private int targetPartition = 0;
        private java.util.List<ProbeSpec> probes = java.util.List.of();

        @JsonCreator
        private Builder(@JsonProperty("experimentName") String experimentName) {
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

        public Builder targetAll(boolean v) {
            this.targetAll = v;
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

        /** A null map, as JSON's {@code "envOverrides": null} gives, means none. */
        public Builder envOverrides(Map<String, String> v) {
            this.envOverrides = v != null ? v : Map.of();
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

        public Builder memoryMb(int v) {
            this.memoryMb = v;
            return this;
        }

        public Builder ioWorkers(int v) {
            this.ioWorkers = v;
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

        /** A null list, as JSON's {@code "probes": null} gives, means none. */
        public Builder probes(java.util.List<ProbeSpec> v) {
            this.probes = v != null ? v : java.util.List.of();
            return this;
        }

        public FaultSpec build() {
            return new FaultSpec(
                    experimentName,
                    targetNamespace,
                    targetLabel,
                    targetPod,
                    targetAll,
                    chaosDurationSec,
                    delayBeforeSec,
                    Map.copyOf(envOverrides),
                    disruptionType,
                    targetBrokerId,
                    networkLatencyMs,
                    fillPercentage,
                    cpuCores,
                    memoryMb,
                    ioWorkers,
                    gracePeriodSec,
                    targetTopic,
                    targetPartition,
                    java.util.List.copyOf(probes));
        }
    }
}
