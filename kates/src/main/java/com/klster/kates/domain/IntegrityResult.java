package com.klster.kates.domain;

import com.klster.kates.engine.AckTracker;

import java.time.Duration;
import java.util.List;

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
        long unackedLost,
        long duplicateRecords,
        List<LostRange> lostRanges,
        Duration producerRto,
        Duration consumerRto,
        Duration maxRto,
        Duration rpo,
        double dataLossPercent,
        List<AckTracker.FailureWindow> failureWindows
) {
    public static Builder builder() { return new Builder(); }

    public static class Builder {
        private long totalSent;
        private long totalAcked;
        private long totalConsumed;
        private long lostRecords;
        private long unackedLost;
        private long duplicateRecords;
        private List<LostRange> lostRanges = List.of();
        private Duration producerRto = Duration.ZERO;
        private Duration consumerRto = Duration.ZERO;
        private Duration maxRto = Duration.ZERO;
        private Duration rpo = Duration.ZERO;
        private double dataLossPercent;
        private List<AckTracker.FailureWindow> failureWindows = List.of();

        public Builder totalSent(long v) { this.totalSent = v; return this; }
        public Builder totalAcked(long v) { this.totalAcked = v; return this; }
        public Builder totalConsumed(long v) { this.totalConsumed = v; return this; }
        public Builder lostRecords(long v) { this.lostRecords = v; return this; }
        public Builder unackedLost(long v) { this.unackedLost = v; return this; }
        public Builder duplicateRecords(long v) { this.duplicateRecords = v; return this; }
        public Builder lostRanges(List<LostRange> v) { this.lostRanges = v; return this; }
        public Builder producerRto(Duration v) { this.producerRto = v; return this; }
        public Builder consumerRto(Duration v) { this.consumerRto = v; return this; }
        public Builder maxRto(Duration v) { this.maxRto = v; return this; }
        public Builder rpo(Duration v) { this.rpo = v; return this; }
        public Builder dataLossPercent(double v) { this.dataLossPercent = v; return this; }
        public Builder failureWindows(List<AckTracker.FailureWindow> v) { this.failureWindows = v; return this; }

        public IntegrityResult build() {
            return new IntegrityResult(totalSent, totalAcked, totalConsumed,
                    lostRecords, unackedLost, duplicateRecords, lostRanges,
                    producerRto, consumerRto, maxRto, rpo, dataLossPercent,
                    failureWindows);
        }
    }
}
