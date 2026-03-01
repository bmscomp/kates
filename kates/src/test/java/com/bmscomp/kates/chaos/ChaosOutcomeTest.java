package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import java.time.Duration;
import java.time.Instant;

import org.junit.jupiter.api.Test;

class ChaosOutcomeTest {

    @Test
    void successOutcomeIsPass() {
        Instant start = Instant.now().minusSeconds(30);
        Instant end = Instant.now();

        ChaosOutcome outcome = ChaosOutcome.success(
                "engine-1", "pod-delete", start, end, System.nanoTime(), "100%", null, "Complete");

        assertTrue(outcome.isPass());
        assertEquals("Pass", outcome.verdict());
        assertNull(outcome.failureReason());
        assertEquals("engine-1", outcome.engineName());
        assertEquals("pod-delete", outcome.experimentName());
        assertEquals(Duration.between(start, end), outcome.chaosDuration());
    }

    @Test
    void failureOutcomeIsNotPass() {
        Instant now = Instant.now();

        ChaosOutcome outcome = ChaosOutcome.failure(
                "engine-2", "cpu-hog", now, now, System.nanoTime(),
                "Timeout", "50%", "ChaosRevert", "Aborted");

        assertFalse(outcome.isPass());
        assertEquals("Fail", outcome.verdict());
        assertEquals("Timeout", outcome.failureReason());
        assertEquals("Aborted", outcome.phase());
    }

    @Test
    void skippedOutcomeIsNotPass() {
        ChaosOutcome outcome = ChaosOutcome.skipped("Provider unavailable");

        assertFalse(outcome.isPass());
        assertEquals("Skipped", outcome.verdict());
        assertEquals("Provider unavailable", outcome.failureReason());
        assertEquals("none", outcome.engineName());
    }

    @Test
    void isPassIsCaseInsensitive() {
        Instant now = Instant.now();
        ChaosOutcome outcome = new ChaosOutcome(
                "e", "x", now, now, 0, Duration.ZERO, "PASS", null, null, null, null);
        assertTrue(outcome.isPass());
    }

    @Test
    void nonPassVerdictReturnsFalse() {
        Instant now = Instant.now();
        ChaosOutcome outcome = new ChaosOutcome(
                "e", "x", now, now, 0, Duration.ZERO, "Error", null, null, null, null);
        assertFalse(outcome.isPass());
    }
}
