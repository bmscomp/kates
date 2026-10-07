package com.bmscomp.kates.chaos;

/**
 * A probe that could not be evaluated: its command failed or ran out of time,
 * its output is not what its comparator compares, or it names a check or a
 * comparator that does not exist. {@link ProbeExecutor} turns it into a failed
 * result, never into a pass.
 */
final class ProbeFailure extends RuntimeException {

    ProbeFailure(String message) {
        super(message);
    }

    ProbeFailure(String message, Throwable cause) {
        super(message, cause);
    }
}
