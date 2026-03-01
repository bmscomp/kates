package com.bmscomp.kates.engine;

/**
 * Thrown when a backend operation fails.
 */
public class BenchmarkException extends RuntimeException {

    public BenchmarkException(String message) {
        super(message);
    }

    public BenchmarkException(String message, Throwable cause) {
        super(message, cause);
    }
}
