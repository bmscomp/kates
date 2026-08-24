package com.bmscomp.kates.engine;

/**
 * Thrown when a test cannot start because {@code kates.engine.max-concurrent-tests}
 * runs are already in flight.
 *
 * <p>Distinct from a plain {@link BenchmarkException} so the API can answer 429
 * (retry this later) instead of 400 (your request was wrong). The cap became
 * genuinely enforced in this wave, so clients now hit it in normal operation and
 * need to tell the two apart to know whether retrying makes sense.
 */
public class ConcurrencyLimitException extends BenchmarkException {

    private final int maxConcurrentTests;

    public ConcurrencyLimitException(int maxConcurrentTests) {
        super("Concurrency limit reached: " + maxConcurrentTests
                + " tests already running. Retry later or increase kates.engine.max-concurrent-tests.");
        this.maxConcurrentTests = maxConcurrentTests;
    }

    public int getMaxConcurrentTests() {
        return maxConcurrentTests;
    }
}
