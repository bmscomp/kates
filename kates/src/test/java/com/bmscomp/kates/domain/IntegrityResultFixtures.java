package com.bmscomp.kates.domain;

import java.time.Duration;
import java.util.List;

import com.bmscomp.kates.engine.AckTracker;

/** Integrity results for tests that store one and read it back. */
public final class IntegrityResultFixtures {

    private IntegrityResultFixtures() {}

    /**
     * A result with every component set: lost ranges, a failure window and a
     * timeline, and an RTO and RPO with nanosecond parts, so that a read-back
     * that drops or rounds any of them does not compare equal.
     */
    public static IntegrityResult full() {
        return withRpo(Duration.ofNanos(250_000_789));
    }

    /** {@link #full()} with this RPO; {@code null} is an RPO not measured. */
    public static IntegrityResult withRpo(Duration rpo) {
        return new IntegrityResult(
                100_000,
                100_000,
                100_001,
                2,
                3,
                0.002,
                List.of(new LostRange(45_231, 45_231, 1), new LostRange(78_442, 78_442, 1)),
                Duration.ofNanos(1_500_000_123),
                Duration.ofNanos(800_000_456),
                Duration.ofNanos(1_500_000_123),
                rpo,
                List.of(new AckTracker.FailureWindow(1_000, 1_500_001_123)),
                4,
                5,
                true,
                true,
                true,
                false,
                List.of(
                        new IntegrityEvent(1_767_970_803_011L, "LOST_RANGE", "seq 45231-45231 (1 records)"),
                        new IntegrityEvent(1_767_970_803_012L, "SUMMARY", "verdict=DATA_LOSS lost=2 duplicates=3")));
    }
}
