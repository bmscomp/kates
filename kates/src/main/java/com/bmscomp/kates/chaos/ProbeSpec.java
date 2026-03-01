package com.bmscomp.kates.chaos;

/**
 * Generic descriptor for an assertion/probe to be evaluated during chaos.
 * Maps to backend-specific probes (like Litmus cmdProbe or k8sProbe).
 */
public record ProbeSpec(
        String name,
        String type,
        String mode,
        String command,
        String expectedOutput,
        String comparator,
        int intervalSec,
        int timeoutSec) {

    public static Builder builder(String name) {
        return new Builder(name);
    }

    public static class Builder {
        private final String name;
        private String type = "cmdProbe";
        private String mode = "Edge";
        private String command = "";
        private String expectedOutput = "";
        private String comparator = "contains";
        private int intervalSec = 10;
        private int timeoutSec = 30;

        private Builder(String name) {
            this.name = name;
        }

        public Builder type(String v) { this.type = v; return this; }
        public Builder mode(String v) { this.mode = v; return this; }
        public Builder command(String v) { this.command = v; return this; }
        public Builder expectedOutput(String v) { this.expectedOutput = v; return this; }
        public Builder comparator(String v) { this.comparator = v; return this; }
        public Builder intervalSec(int v) { this.intervalSec = v; return this; }
        public Builder timeoutSec(int v) { this.timeoutSec = v; return this; }

        public ProbeSpec build() {
            return new ProbeSpec(name, type, mode, command, expectedOutput, comparator, intervalSec, timeoutSec);
        }
    }
}
