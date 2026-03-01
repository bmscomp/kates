package com.bmscomp.kates.chaos;

import java.util.concurrent.CompletableFuture;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Named;

import org.jboss.logging.Logger;

/**
 * No-op chaos provider for environments without a chaos backend.
 * Used in tests and for manual fault injection workflows where the
 * operator triggers faults externally while Kates measures the impact.
 */
@ApplicationScoped
@Named("noop")
public class NoOpChaosProvider implements ChaosProvider {

    private static final Logger LOG = Logger.getLogger(NoOpChaosProvider.class);

    @Override
    public String name() {
        return "noop";
    }

    @Override
    public CompletableFuture<ChaosOutcome> triggerFault(FaultSpec spec) {
        LOG.info("NoOp chaos provider: fault injection skipped for " + spec.experimentName());
        return CompletableFuture.completedFuture(
                ChaosOutcome.skipped("No chaos provider configured — inject faults manually"));
    }

    @Override
    public ChaosStatus pollStatus(String engineName) {
        return ChaosStatus.NOT_FOUND;
    }

    @Override
    public void cleanup(String engineName) {
        // nothing to clean up
    }

    @Override
    public boolean isAvailable() {
        return true;
    }
}
