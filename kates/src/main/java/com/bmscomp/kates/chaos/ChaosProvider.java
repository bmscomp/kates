package com.bmscomp.kates.chaos;

import java.util.concurrent.CompletableFuture;

/**
 * Service Provider Interface for chaos experiment backends.
 * Implementations translate {@link FaultSpec} into backend-specific
 * operations (Litmus CRDs, kubectl, Chaos Mesh, etc.).
 *
 * <p>To add a new chaos backend:
 * <ol>
 *   <li>Implement this interface</li>
 *   <li>Annotate with {@code @Named("your-backend")}</li>
 *   <li>Set {@code kates.chaos.provider=your-backend} in config</li>
 * </ol>
 */
public interface ChaosProvider {

    /**
     * Returns the provider name (e.g. "litmus-crd", "noop").
     */
    String name();

    /**
     * The {@code kates.chaos.provider} value that selects this provider.
     *
     * <p>Defaults to {@link #name()}. A provider whose name carries runtime
     * detail must override it: the hybrid provider reports itself as
     * {@code hybrid(litmus-crd)}, a string no one can put in config, and while
     * selection compared against {@code name()} it could never be chosen.
     */
    default String id() {
        return name();
    }

    /**
     * Triggers a fault injection asynchronously.
     * The returned future resolves when the chaos experiment completes.
     */
    CompletableFuture<ChaosOutcome> triggerFault(FaultSpec spec);

    /**
     * Checks whether a specific chaos engine is still running.
     */
    ChaosStatus pollStatus(String engineName);

    /**
     * Cleans up resources after an experiment completes.
     */
    void cleanup(String engineName);

    /**
     * Returns true if this provider is available and properly configured.
     */
    boolean isAvailable();

    enum ChaosStatus {
        NOT_FOUND,
        PENDING,
        RUNNING,
        COMPLETED,
        FAILED
    }
}
