package com.klster.kates.chaos;

import java.util.concurrent.CompletableFuture;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.inject.Named;

import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.dsl.base.CustomResourceDefinitionContext;
import org.jboss.logging.Logger;

/**
 * Auto-detecting hybrid chaos provider. At startup, checks whether
 * Litmus CRDs exist in the cluster:
 * <ul>
 *   <li>If Litmus is available → delegates to {@link LitmusChaosProvider}</li>
 *   <li>Otherwise → delegates to {@link KubernetesChaosProvider}</li>
 * </ul>
 *
 * <p>This is the recommended default provider ({@code kates.chaos.provider=hybrid}).
 */
@ApplicationScoped
@Named("hybrid")
public class HybridChaosProvider implements ChaosProvider {

    private static final Logger LOG = Logger.getLogger(HybridChaosProvider.class);

    private final ChaosProvider delegate;
    private final String delegateName;

    @Inject
    public HybridChaosProvider(
            KubernetesClient client,
            @Named("litmus-crd") LitmusChaosProvider litmusProvider,
            @Named("kubernetes") KubernetesChaosProvider kubernetesProvider) {

        if (isLitmusAvailable(client)) {
            this.delegate = litmusProvider;
            this.delegateName = "litmus-crd";
            LOG.info("Hybrid provider: Litmus CRDs detected → using litmus-crd backend");
        } else {
            this.delegate = kubernetesProvider;
            this.delegateName = "kubernetes";
            LOG.info("Hybrid provider: Litmus not found → using direct kubernetes backend");
        }
    }

    private boolean isLitmusAvailable(KubernetesClient client) {
        try {
            CustomResourceDefinitionContext ctx = new CustomResourceDefinitionContext.Builder()
                    .withGroup("litmuschaos.io")
                    .withVersion("v1alpha1")
                    .withPlural("chaosengines")
                    .withScope("Namespaced")
                    .build();
            client.genericKubernetesResources(ctx).inAnyNamespace().list();
            return true;
        } catch (Exception e) {
            return false;
        }
    }

    @Override
    public String name() {
        return "hybrid(" + delegateName + ")";
    }

    @Override
    public CompletableFuture<ChaosOutcome> triggerFault(FaultSpec spec) {
        return delegate.triggerFault(spec);
    }

    @Override
    public ChaosStatus pollStatus(String engineName) {
        return delegate.pollStatus(engineName);
    }

    @Override
    public void cleanup(String engineName) {
        delegate.cleanup(engineName);
    }

    @Override
    public boolean isAvailable() {
        return delegate.isAvailable();
    }
}
