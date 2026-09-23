package com.bmscomp.kates.domain;

import java.time.Duration;
import java.util.List;

import com.fasterxml.jackson.annotation.JsonProperty;

import com.bmscomp.kates.engine.AckTracker;

/**
 * Result of a data integrity verification: how many records were lost,
 * what the RTO/RPO values are, and which sequence ranges are missing.
 *
 * <p>Tracks both producer-side and consumer-side RTO for a complete picture
 * of recovery time from the perspective of both writers and readers.
 *
 * <p><b>The millisecond accessors and {@link #verdict()} are part of the wire
 * format.</b> Jackson serializes a record's components and nothing else, so
 * without {@code @JsonProperty} the JSON carried only the {@code Duration}
 * components (as decimal seconds) and none of the {@code *Ms} keys or the
 * verdict. The CLI reads exactly those keys: its {@code maxRtoMs} and
 * {@code maxRpoMs} gates compared thresholds against a value that was always
 * absent, so they could never fail, and {@code kates test get} never showed
 * RTO, RPO or the verdict.
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
    @JsonProperty("producerRtoMs")
    public double producerRtoMs() {
        return producerRto != null ? producerRto.toNanos() / 1_000_000.0 : 0;
    }

    @JsonProperty("consumerRtoMs")
    public double consumerRtoMs() {
        return consumerRto != null ? consumerRto.toNanos() / 1_000_000.0 : 0;
    }

    @JsonProperty("maxRtoMs")
    public double maxRtoMs() {
        return maxRto != null ? maxRto.toNanos() / 1_000_000.0 : 0;
    }

    /**
     * RPO in milliseconds, or {@code -1} when RPO was not measured ({@code rpo}
     * is null because the verifier had no chaos start time to measure from).
     * Negative means "unknown", never "nothing at risk" — the convention
     * {@code SlaEvaluator} and the CLI both use to skip an RPO constraint
     * rather than pass it.
     */
    @JsonProperty("rpoMs")
    public double rpoMs() {
        return rpo != null ? rpo.toNanos() / 1_000_000.0 : -1;
    }

    @JsonProperty("verdict")
    public String verdict() {
        if (lostRecords > 0) return "DATA_LOSS";
        if (crcFailures > 0) return "CORRUPTION";
        if (outOfOrderCount > 0) return "ORDERING_VIOLATION";
        if (duplicateRecords > 0) return "DUPLICATES_DETECTED";
        return "PASS";
    }
}
