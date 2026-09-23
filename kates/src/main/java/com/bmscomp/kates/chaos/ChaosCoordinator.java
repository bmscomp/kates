package com.bmscomp.kates.chaos;

import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;

import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

/**
 * CDI coordinator that delegates to the configured {@link ChaosProvider}.
 * Provider selection is driven by {@code kates.chaos.provider} config property,
 * matched against {@link ChaosProvider#id()}.
 *
 * <p>Falls back to the {@code noop} provider if the configured provider is
 * unknown or unavailable, and logs that at ERROR: noop skips every experiment,
 * so a fallback turns every chaos run into one that injected nothing.
 */
@ApplicationScoped
public class ChaosCoordinator {

    private static final Logger LOG = Logger.getLogger(ChaosCoordinator.class);

    private static final String NOOP = "noop";

    private final ChaosProvider activeProvider;

    @Inject
    public ChaosCoordinator(
            Instance<ChaosProvider> providers,
            @ConfigProperty(name = "kates.chaos.provider", defaultValue = NOOP) String providerName) {

        ChaosProvider selected = null;
        ChaosProvider fallback = null;
        List<String> known = new ArrayList<>();

        for (ChaosProvider p : providers) {
            known.add(p.id());
            if (p.id().equals(providerName)) {
                selected = p;
            }
            if (p.id().equals(NOOP)) {
                fallback = p;
            }
        }

        if (selected != null && selected.isAvailable()) {
            this.activeProvider = selected;
            LOG.info("Chaos provider: " + selected.name());
            return;
        }

        this.activeProvider = fallback != null ? fallback : new NoOpChaosProvider();
        if (NOOP.equals(providerName)) {
            LOG.info("Chaos provider: noop");
        } else if (selected != null) {
            LOG.errorf(
                    "Chaos provider '%s' (kates.chaos.provider) is not available; falling back to noop."
                            + " Fault injection is DISABLED: every chaos experiment will be skipped.",
                    providerName);
        } else {
            LOG.errorf(
                    "kates.chaos.provider='%s' matches no chaos provider (known: %s); falling back to noop."
                            + " Fault injection is DISABLED: every chaos experiment will be skipped.",
                    providerName, known);
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

    /** False when the active provider is noop, which skips every experiment. */
    public boolean injectsFaults() {
        return !NOOP.equals(activeProvider.id());
    }

    /**
     * Cleans up a specific chaos engine.
     */
    public void cleanup(String engineName) {
        activeProvider.cleanup(engineName);
    }
}
