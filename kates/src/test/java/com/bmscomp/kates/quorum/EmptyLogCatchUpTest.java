package com.bmscomp.kates.quorum;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.time.Instant;

import org.junit.jupiter.api.Test;

class EmptyLogCatchUpTest {

    private final EmptyLogCatchUp catchUp = new EmptyLogCatchUp();

    private static Instant at(long seconds) {
        return Instant.EPOCH.plusSeconds(seconds);
    }

    @Test
    void asksAfterFifteenSecondsWithAnEmptyLog() {
        assertFalse(catchUp.decide(at(0), true));
        assertFalse(catchUp.decide(at(10), true));
        assertTrue(catchUp.decide(at(15), true));
    }

    @Test
    void asksAgainOnlyAMinuteLater() {
        catchUp.decide(at(0), true);
        assertTrue(catchUp.decide(at(15), true));
        assertFalse(catchUp.decide(at(20), true));
        assertFalse(catchUp.decide(at(70), true));
        assertTrue(catchUp.decide(at(75), true));
    }

    @Test
    void aLogThatFillsStartsTheClockOver() {
        catchUp.decide(at(0), true);
        assertFalse(catchUp.decide(at(10), false));
        assertFalse(catchUp.decide(at(15), true));
        assertTrue(catchUp.decide(at(30), true));
    }
}
