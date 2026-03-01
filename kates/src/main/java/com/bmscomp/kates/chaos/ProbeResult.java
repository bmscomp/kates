package com.bmscomp.kates.chaos;

import java.time.Instant;

/**
 * Result of evaluating a probe during a resilience test.
 */
public record ProbeResult(
        String name,
        boolean passed,
        String output,
        long durationMs,
        Instant evaluatedAt) {

    public static ProbeResult pass(String name, String output, long durationMs) {
        return new ProbeResult(name, true, output, durationMs, Instant.now());
    }

    public static ProbeResult fail(String name, String output, long durationMs) {
        return new ProbeResult(name, false, output, durationMs, Instant.now());
    }

    public static ProbeResult error(String name, String message) {
        return new ProbeResult(name, false, message, 0, Instant.now());
    }
}
