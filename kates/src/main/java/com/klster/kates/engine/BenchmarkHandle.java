package com.klster.kates.engine;

/**
 * Opaque handle returned by {@link BenchmarkBackend#submit}.
 * Carries enough information for the backend to poll/stop the task later.
 */
public record BenchmarkHandle(String backendName, String taskId, Object internalRef) {

    public BenchmarkHandle(String backendName, String taskId) {
        this(backendName, taskId, null);
    }
}
