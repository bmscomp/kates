package com.bmscomp.kates.quorum;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.time.Duration;
import java.time.Instant;

import org.junit.jupiter.api.Test;

class DatabaseHandoverTest {

    private static final Instant T0 = Instant.parse("2026-11-15T12:00:00Z");

    private static Instant at(int seconds) {
        return T0.plus(Duration.ofSeconds(seconds));
    }

    @Test
    void handsOverAfter15sWithNothingLive() {
        DatabaseHandover handover = new DatabaseHandover();
        assertFalse(handover.decide(at(0), false, 0));
        assertFalse(handover.decide(at(10), false, 0));
        assertTrue(handover.decide(at(15), false, 0));
    }

    @Test
    void aLiveRunWaitsUpTo2Minutes() {
        DatabaseHandover handover = new DatabaseHandover();
        assertFalse(handover.decide(at(0), false, 1));
        assertFalse(handover.decide(at(60), false, 1));
        assertTrue(handover.decide(at(61), false, 0), "the run ended: hand over");
        DatabaseHandover stuck = new DatabaseHandover();
        stuck.decide(at(0), false, 1);
        assertFalse(stuck.decide(at(119), false, 1));
        assertTrue(stuck.decide(at(120), false, 1));
    }

    @Test
    void handsOverAtMostOnceIn2Minutes() {
        DatabaseHandover handover = new DatabaseHandover();
        handover.decide(at(0), false, 0);
        assertTrue(handover.decide(at(15), false, 0));
        // Leading again, as every member lacks the database.
        assertFalse(handover.decide(at(30), false, 0));
        assertFalse(handover.decide(at(134), false, 0));
        assertTrue(handover.decide(at(135), false, 0));
    }

    @Test
    void theDatabaseBackResetsTheClock() {
        DatabaseHandover handover = new DatabaseHandover();
        handover.decide(at(0), false, 0);
        assertFalse(handover.decide(at(10), true, 0));
        assertFalse(handover.decide(at(20), false, 0));
        assertTrue(handover.decide(at(35), false, 0));
    }
}
