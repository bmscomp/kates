package com.bmscomp.kates.chaos;

import java.util.concurrent.CompletableFuture;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;

import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

/**
 * CDI coordinator that delegates to the configured {@link ChaosProvider}.
 * Provider selection is driven by {@code kates.chaos.provider} config property.
 *
 * <p>Falls back to the {@code noop} provider if the configured provider is unavailable.
 */
@ApplicationScoped
public class ChaosCoordinator {

    private static final Logger LOG = Logger.getLogger(ChaosCoordinator.class);

    private final ChaosProvider activeProvider;

    @Inject
    public ChaosCoordinator(
            Instance<ChaosProvider> providers,
            @ConfigProperty(name = "kates.chaos.provider", defaultValue = "noop") String providerName) {

        ChaosProvider selected = null;
        ChaosProvider fallback = null;

        for (ChaosProvider p : providers) {
            if (p.name().equals(providerName)) {
                selected = p;
            }
            if (p.name().equals("noop")) {
                fallback = p;
            }
        }

        if (selected != null && selected.isAvailable()) {
            this.activeProvider = selected;
            LOG.info("Chaos provider: " + selected.name());
        } else {
            this.activeProvider = fallback != null ? fallback : new NoOpChaosProvider();
            if (selected != null) {
                LOG.warn("Configured chaos provider '" + providerName + "' is not available, falling back to noop");
            } else {
                LOG.info("Chaos provider: noop (default)");
            }
        }
    }

    /**
     * Triggers a fault injection using the active provider.
     */
    public CompletableFuture<ChaosOutcome> triggerFault(FaultSpec spec) {
        return activeProvider.triggerFault(spec);
    }

    /**
     * Returns the name of the active provider.
     */
    public String activeProviderName() {
        return activeProvider.name();
    }

    /**
     * Cleans up a specific chaos engine.
     */
    public void cleanup(String engineName) {
        activeProvider.cleanup(engineName);
    }
}
