package com.bmscomp.kates.domain;

/**
 * Describes a contiguous range of lost records detected during data integrity verification.
 */
public record LostRange(long fromSeq, long toSeq, long count) {
    public LostRange {
        if (fromSeq > toSeq) throw new IllegalArgumentException("fromSeq must be <= toSeq");
        if (count < 0) throw new IllegalArgumentException("count must be >= 0");
    }
}
