package com.klster.kates.chaos;

import java.time.Duration;
import java.time.Instant;

/**
 * Result of a completed chaos experiment, including precise timestamps
 * needed for RPO/RTO computation.
 *
 * <p>Maintains both wall-clock ({@link Instant}) and monotonic ({@link System#nanoTime()})
 * timestamps. Use wall-clock for reporting and monotonic for RPO computation
 * against {@link com.klster.kates.engine.AckTracker} nano timestamps.
 */
public record ChaosOutcome(
        String engineName,
        String experimentName,
        Instant chaosStartTime,
        Instant chaosEndTime,
        long chaosStartNanos,
        Duration chaosDuration,
        String verdict,
        String failureReason
) {
    public boolean isPass() {
        return "Pass".equalsIgnoreCase(verdict);
    }

    public static ChaosOutcome success(String engineName, String experimentName,
                                       Instant start, Instant end, long startNanos) {
        return new ChaosOutcome(engineName, experimentName, start, end, startNanos,
                Duration.between(start, end), "Pass", null);
    }

    public static ChaosOutcome failure(String engineName, String experimentName,
                                       Instant start, Instant end, long startNanos, String reason) {
        return new ChaosOutcome(engineName, experimentName, start, end, startNanos,
                Duration.between(start, end), "Fail", reason);
    }

    public static ChaosOutcome skipped(String reason) {
        Instant now = Instant.now();
        return new ChaosOutcome("none", "none", now, now, System.nanoTime(),
                Duration.ZERO, "Skipped", reason);
    }
}
