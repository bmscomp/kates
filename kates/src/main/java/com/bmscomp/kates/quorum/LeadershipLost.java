package com.bmscomp.kates.quorum;

import java.time.Instant;

/**
 * This member led {@code term} and no longer does: another member leads, or none yet. It stops its
 * own runs; undoing its faults is the next leader's.
 */
public record LeadershipLost(long term, Instant at) {}
