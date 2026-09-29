package com.bmscomp.kates.engine;

/**
 * SPI for benchmark execution backends.
 *
 * Implementations are CDI beans discovered at runtime.
 * The {@link #name()} is used to select a backend via the API
 * or the {@code kates.engine.default-backend} config property.
 */
public interface BenchmarkBackend {

    /** Unique backend identifier (e.g. "trogdor", "native", "cli"). */
    String name();

    /** Submit a task for execution. Returns a handle for polling and stopping. */
    BenchmarkHandle submit(BenchmarkTask task);

    /** Poll the current status of a previously submitted task. */
    BenchmarkStatus poll(BenchmarkHandle handle);

    /** Request graceful stop of a running task. */
    void stop(BenchmarkHandle handle);

    /**
     * Tells a running task that a fault was injected at {@code chaosStartNanos},
     * a {@link System#nanoTime()} reading from this JVM, so an integrity task
     * can measure RPO from it. Only an in-process backend shares that clock;
     * the default ignores it.
     */
    default void markChaosStart(BenchmarkHandle handle, long chaosStartNanos) {}
}
