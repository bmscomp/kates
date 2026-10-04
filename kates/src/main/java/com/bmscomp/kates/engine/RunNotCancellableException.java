package com.bmscomp.kates.engine;

import com.bmscomp.kates.domain.TestResult;

/**
 * Thrown by {@link TestOrchestrator#cancelTest(String)} when the run is
 * neither PENDING nor RUNNING, including one that ended while the cancel was
 * being made.
 *
 * <p>Its own type so REST can answer 409 and gRPC FAILED_PRECONDITION for this
 * outcome alone. Both used to catch any {@link IllegalStateException}, which
 * would have turned an unrelated one from persistence or CDI into a conflict
 * carrying an internal message.
 */
public class RunNotCancellableException extends RuntimeException {

    private final TestResult.TaskStatus status;

    public RunNotCancellableException(TestResult.TaskStatus status) {
        super("Test is not running (status: " + status + ")");
        this.status = status;
    }

    /** The status the run is stored in. */
    public TestResult.TaskStatus getStatus() {
        return status;
    }
}
