package com.klster.kates.domain;

import com.fasterxml.jackson.annotation.JsonInclude;

/**
 * A timestamped event during integrity verification.
 * Used to build a diagnostic timeline of CRC failures, ordering violations, and lost ranges.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public record IntegrityEvent(
        long timestampMs,
        String type,
        String detail
) {
    public static final String CRC_FAILURE = "CRC_FAILURE";
    public static final String OUT_OF_ORDER = "OUT_OF_ORDER";
    public static final String LOST_RANGE = "LOST_RANGE";
    public static final String SUMMARY = "SUMMARY";

    public static IntegrityEvent crcFailure(int partition, long sequence) {
        return new IntegrityEvent(System.currentTimeMillis(), CRC_FAILURE,
                "partition=" + partition + " seq=" + sequence);
    }

    public static IntegrityEvent outOfOrder(int partition, long expected, long actual) {
        return new IntegrityEvent(System.currentTimeMillis(), OUT_OF_ORDER,
                "partition=" + partition + " expected=" + expected + " actual=" + actual);
    }

    public static IntegrityEvent lostRange(long fromSeq, long toSeq, long count) {
        return new IntegrityEvent(System.currentTimeMillis(), LOST_RANGE,
                "from=" + fromSeq + " to=" + toSeq + " count=" + count);
    }

    public static IntegrityEvent summary(String verdict, long lost, long duplicates) {
        return new IntegrityEvent(System.currentTimeMillis(), SUMMARY,
                "verdict=" + verdict + " lost=" + lost + " duplicates=" + duplicates);
    }
}
