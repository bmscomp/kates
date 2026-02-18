package com.klster.kates.disruption;

import java.time.Duration;
import java.time.Instant;
import java.util.List;

/**
 * In-Sync Replica tracking records for disruption intelligence.
 */
public final class IsrSnapshot {

    private IsrSnapshot() {}

    /**
     * A single point-in-time ISR observation for one partition.
     */
    public record Entry(
            Instant timestamp, String topic, int partition, int leaderId, List<Integer> isr, int replicationFactor) {
        public boolean isFullyReplicated() {
            return isr != null && isr.size() >= replicationFactor;
        }

        public int isrDepth() {
            return isr != null ? isr.size() : 0;
        }
    }

    /**
     * Aggregated ISR metrics computed after a disruption step.
     */
    public record Metrics(
            Duration timeToFullIsr,
            int minIsrDepth,
            int underReplicatedPeakCount,
            int totalPartitions,
            List<Entry> timeline) {
        public boolean recoveredFully() {
            return timeToFullIsr != null;
        }
    }
}
