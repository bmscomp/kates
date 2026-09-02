package com.bmscomp.kates.chaos;

import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;

import org.jboss.logging.Logger;

/**
 * Orchestrates compound fault injection by executing multiple chaos providers
 * simultaneously. Enables complex failure scenarios like
 * "network partition + CPU stress" or "pod kill + disk fill".
 */
@ApplicationScoped
public class CompoundChaosOrchestrator {

    private static final Logger LOG = Logger.getLogger(CompoundChaosOrchestrator.class);

    @Inject
    Instance<ChaosProvider> providers;

    public record CompoundFault(FaultSpec spec, String providerName) {}

    public record CompoundOutcome(boolean allSucceeded, List<ProviderOutcome> results) {}

    public record ProviderOutcome(String providerName, String experimentName, boolean succeeded, String message) {}

    /**
     * Executes multiple faults in parallel across potentially different providers.
     * Each fault is paired with its target provider name.
     */
    public CompoundOutcome executeConcurrent(List<CompoundFault> faults, int timeoutSec) {
        List<CompletableFuture<ProviderOutcome>> futures = new ArrayList<>();

        for (CompoundFault fault : faults) {
            ChaosProvider provider = resolveProvider(fault.providerName());
            if (provider == null) {
                futures.add(CompletableFuture.completedFuture(new ProviderOutcome(
                        fault.providerName(),
                        fault.spec().experimentName(),
                        false,
                        "Provider not found: " + fault.providerName())));
                continue;
            }

            CompletableFuture<ProviderOutcome> future = provider.triggerFault(fault.spec())
                    .thenApply(outcome -> new ProviderOutcome(
                            fault.providerName(),
                            fault.spec().experimentName(),
                            outcome.isPass(),
                            outcome.failureReason()))
                    .exceptionally(ex -> new ProviderOutcome(
                            fault.providerName(), fault.spec().experimentName(), false, ex.getMessage()));

            futures.add(future);
        }

        List<ProviderOutcome> results = new ArrayList<>();
        boolean allOk = true;

        for (CompletableFuture<ProviderOutcome> f : futures) {
            try {
                ProviderOutcome outcome = f.get(timeoutSec, TimeUnit.SECONDS);
                results.add(outcome);
                if (!outcome.succeeded()) allOk = false;
            } catch (TimeoutException e) {
                results.add(new ProviderOutcome("unknown", "unknown", false, "Timed out after " + timeoutSec + "s"));
                allOk = false;
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                results.add(new ProviderOutcome("unknown", "unknown", false, "Interrupted"));
                allOk = false;
            } catch (ExecutionException e) {
                results.add(new ProviderOutcome(
                        "unknown", "unknown", false, e.getCause().getMessage()));
                allOk = false;
            }
        }

        return new CompoundOutcome(allOk, results);
    }

    /**
     * Executes faults sequentially with configurable delay between each.
     */
    public CompoundOutcome executeSequential(List<CompoundFault> faults, int delayBetweenSec) {
        List<ProviderOutcome> results = new ArrayList<>();
        boolean allOk = true;

        for (CompoundFault fault : faults) {
            ChaosProvider provider = resolveProvider(fault.providerName());
            if (provider == null) {
                results.add(new ProviderOutcome(
                        fault.providerName(), fault.spec().experimentName(), false, "Provider not found"));
                allOk = false;
                continue;
            }

            try {
                ChaosOutcome outcome = provider.triggerFault(fault.spec()).get(120, TimeUnit.SECONDS);
                results.add(new ProviderOutcome(
                        fault.providerName(), fault.spec().experimentName(),
                        outcome.isPass(), outcome.failureReason()));
                if (!outcome.isPass()) allOk = false;
            } catch (Exception e) {
                results.add(new ProviderOutcome(
                        fault.providerName(), fault.spec().experimentName(), false, e.getMessage()));
                allOk = false;
            }

            if (delayBetweenSec > 0) {
                try {
                    Thread.sleep(delayBetweenSec * 1000L);
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                    break;
                }
            }
        }
        return new CompoundOutcome(allOk, results);
    }

    /**
     * Lists all available chaos providers.
     */
    public List<String> availableProviders() {
        List<String> names = new ArrayList<>();
        // Handles, not a plain for-each over the Instance: iterating it
        // INSTANTIATES each provider, and a provider whose construction fails
        // (HybridChaosProvider needs a KubernetesClient, which there is no way
        // to build with no kubeconfig and no service account) propagates out of
        // the iterator. That turned "list the providers" into a 500 on every
        // machine without a cluster — including CI, while passing on a laptop
        // that happened to have a kubeconfig. A provider that cannot even be
        // created is exactly what this endpoint exists to report.
        for (Instance.Handle<ChaosProvider> handle : providers.handles()) {
            try {
                ChaosProvider p = handle.get();
                names.add(p.name() + (p.isAvailable() ? " (available)" : " (unavailable)"));
            } catch (Exception e) {
                LOG.warnf("Chaos provider could not be inspected, omitting it: %s", e.getMessage());
            }
        }
        return names;
    }

    private ChaosProvider resolveProvider(String name) {
        for (ChaosProvider p : providers) {
            if (p.name().equals(name) && p.isAvailable()) return p;
        }
        return null;
    }
}
