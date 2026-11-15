package com.bmscomp.kates.quorum;

import java.time.Instant;

/**
 * Fired, synchronously, when this member first confirms it leads a term, and before it takes any
 * write in that term. An observer that throws keeps the member from taking writes until the next
 * try: runs and faults are recovered first, or nothing new starts.
 *
 * @param at when this member found it leads: everything a member started before then was started
 *     by a previous leader
 */
public record LeadershipAcquired(long term, Instant at) {}
