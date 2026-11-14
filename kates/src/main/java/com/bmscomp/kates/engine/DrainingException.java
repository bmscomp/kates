package com.bmscomp.kates.engine;

/**
 * Thrown when a test cannot start because the Kates API is draining: it lets
 * its live runs finish and starts no new one, so that its pod can be replaced
 * without losing a run.
 *
 * <p>Distinct from {@link ConcurrencyLimitException}: the API answers 503, not
 * 429, since no slot frees up while the drain lasts.
 */
public class DrainingException extends BenchmarkException {

    public static final String MESSAGE = "Kates is draining: no new run is started until the drain is lifted";

    public DrainingException() {
        super(MESSAGE);
    }
}
