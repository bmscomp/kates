package com.klster.kates.disruption;

import java.time.Duration;
import java.time.Instant;
import java.util.List;
import java.util.Map;

/**
 * Consumer lag tracking records for disruption intelligence.
 */
public final class LagSnapshot {

    private LagSnapshot() {}

    /**
     * A single point-in-time lag observation for a consumer group.
     */
    public record Entry(Instant timestamp, String groupId, long totalLag, Map<String, Long> perTopicLag) {}

    /**
     * Aggregated consumer lag metrics computed after a disruption step.
     */
    public record Metrics(long baselineLag, long peakLag, Duration timeToLagRecovery, List<Entry> timeline) {
        public boolean recoveredFully() {
            return timeToLagRecovery != null;
        }

        public long lagSpike() {
            return peakLag - baselineLag;
        }
    }
}
