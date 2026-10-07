package com.bmscomp.kates.engine;

import java.util.List;

/**
 * Thrown when the benchmark backend a run would take is not one the Kates API
 * has.
 *
 * <p>Distinct from a plain {@link BenchmarkException} so the API can tell
 * whose it is to fix. A backend the request names is the caller's, so the API
 * answers 400. The default one, {@code kates.engine.default-backend}, is the
 * Kates API's own configuration, and a request that names none cannot be at
 * fault for it.
 */
public class UnknownBackendException extends BenchmarkException {

    private final String backend;

    public UnknownBackendException(String backend, List<String> available) {
        super("Backend not found: '" + backend + "'. Available: " + available);
        this.backend = backend;
    }

    /** The backend the run would have taken. */
    public String getBackend() {
        return backend;
    }
}
