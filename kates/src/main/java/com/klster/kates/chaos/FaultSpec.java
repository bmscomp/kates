package com.klster.kates.chaos;

import java.util.Map;

/**
 * Immutable descriptor for a fault injection experiment.
 * Backend-agnostic — each {@link ChaosProvider} maps this to its native format.
 */
public record FaultSpec(
        String experimentName,
        String targetNamespace,
        String targetLabel,
        String targetPod,
        int chaosDurationSec,
        int delayBeforeSec,
        Map<String, String> envOverrides
) {
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

        private Builder(String experimentName) {
            this.experimentName = experimentName;
        }

        public Builder targetNamespace(String v) { this.targetNamespace = v; return this; }
        public Builder targetLabel(String v) { this.targetLabel = v; return this; }
        public Builder targetPod(String v) { this.targetPod = v; return this; }
        public Builder chaosDurationSec(int v) { this.chaosDurationSec = v; return this; }
        public Builder delayBeforeSec(int v) { this.delayBeforeSec = v; return this; }
        public Builder envOverrides(Map<String, String> v) { this.envOverrides = v; return this; }

        public FaultSpec build() {
            return new FaultSpec(experimentName, targetNamespace, targetLabel,
                    targetPod, chaosDurationSec, delayBeforeSec, Map.copyOf(envOverrides));
        }
    }
}
