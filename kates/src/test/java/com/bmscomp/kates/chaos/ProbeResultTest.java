package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import org.junit.jupiter.api.Test;

class ProbeResultTest {

    @Test
    void passFactoryReturnsPassed() {
        ProbeResult r = ProbeResult.pass("isr-check", "OK", 42);
        assertTrue(r.passed());
        assertEquals("isr-check", r.name());
        assertEquals("OK", r.output());
        assertEquals(42, r.durationMs());
        assertNotNull(r.evaluatedAt());
    }

    @Test
    void failFactoryReturnsFailed() {
        ProbeResult r = ProbeResult.fail("latency", "Too high", 100);
        assertFalse(r.passed());
        assertEquals("latency", r.name());
        assertEquals("Too high", r.output());
        assertEquals(100, r.durationMs());
    }

    @Test
    void errorFactoryReturnsFalseWithZeroDuration() {
        ProbeResult r = ProbeResult.error("broken", "Timeout occurred");
        assertFalse(r.passed());
        assertEquals("broken", r.name());
        assertEquals("Timeout occurred", r.output());
        assertEquals(0, r.durationMs());
        assertNotNull(r.evaluatedAt());
    }

    @Test
    void recordEquality() {
        var t = java.time.Instant.now();
        ProbeResult a = new ProbeResult("p", true, "ok", 10, t);
        ProbeResult b = new ProbeResult("p", true, "ok", 10, t);
        assertEquals(a, b);
    }
}
