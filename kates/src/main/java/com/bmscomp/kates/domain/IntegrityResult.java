package com.bmscomp.kates.domain;

import java.time.Duration;
import java.util.List;

import com.bmscomp.kates.engine.AckTracker;

/**
 * Result of a data integrity verification: how many records were lost,
 * what the RTO/RPO values are, and which sequence ranges are missing.
 *
 * <p>Tracks both producer-side and consumer-side RTO for a complete picture
 * of recovery time from the perspective of both writers and readers.
 */
public record IntegrityResult(
        long totalSent,
        long totalAcked,
        long totalConsumed,
        long lostRecords,
        long duplicateRecords,
        double dataLossPercent,
        List<LostRange> lostRanges,
        Duration producerRto,
        Duration consumerRto,
        Duration maxRto,
        Duration rpo,
        List<AckTracker.FailureWindow> failureWindows,
        long outOfOrderCount,
        long crcFailures,
        boolean orderingVerified,
        boolean crcVerified,
        boolean idempotenceEnabled,
        boolean transactionsEnabled,
        List<IntegrityEvent> timeline) {
    public double producerRtoMs() {
        return producerRto != null ? producerRto.toNanos() / 1_000_000.0 : 0;
    }

    public double consumerRtoMs() {
        return consumerRto != null ? consumerRto.toNanos() / 1_000_000.0 : 0;
    }

    public double maxRtoMs() {
        return maxRto != null ? maxRto.toNanos() / 1_000_000.0 : 0;
    }

    public double rpoMs() {
        return rpo != null ? rpo.toNanos() / 1_000_000.0 : 0;
    }

    public String verdict() {
        if (lostRecords > 0) return "DATA_LOSS";
        if (crcFailures > 0) return "CORRUPTION";
        if (outOfOrderCount > 0) return "ORDERING_VIOLATION";
        if (duplicateRecords > 0) return "DUPLICATES_DETECTED";
        return "PASS";
    }
}
