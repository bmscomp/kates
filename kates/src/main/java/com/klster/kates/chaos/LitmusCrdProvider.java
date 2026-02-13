package com.klster.kates.chaos;

import io.fabric8.kubernetes.api.model.GenericKubernetesResource;
import io.fabric8.kubernetes.api.model.GenericKubernetesResourceList;
import io.fabric8.kubernetes.api.model.ObjectMetaBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.dsl.base.ResourceDefinitionContext;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.inject.Named;

import java.time.Instant;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;
import java.util.logging.Level;
import java.util.logging.Logger;

/**
 * Litmus CRD-based chaos provider.
 * Creates {@code ChaosEngine} custom resources via the Fabric8 Kubernetes client
 * and watches for corresponding {@code ChaosResult} resources to determine completion.
 */
@ApplicationScoped
@Named("litmus-crd")
public class LitmusCrdProvider implements ChaosProvider {

    private static final Logger LOG = Logger.getLogger(LitmusCrdProvider.class.getName());

    private static final ResourceDefinitionContext CHAOS_ENGINE_CONTEXT =
            new ResourceDefinitionContext.Builder()
                    .withGroup("litmuschaos.io")
                    .withVersion("v1alpha1")
                    .withPlural("chaosengines")
                    .withNamespaced(true)
                    .build();

    private static final ResourceDefinitionContext CHAOS_RESULT_CONTEXT =
            new ResourceDefinitionContext.Builder()
                    .withGroup("litmuschaos.io")
                    .withVersion("v1alpha1")
                    .withPlural("chaosresults")
                    .withNamespaced(true)
                    .build();

    private final KubernetesClient client;

    @Inject
    public LitmusCrdProvider(KubernetesClient client) {
        this.client = client;
    }

    @Override
    public String name() {
        return "litmus-crd";
    }

    @Override
    public CompletableFuture<ChaosOutcome> triggerFault(FaultSpec spec) {
        String engineName = "kates-" + spec.experimentName() + "-" + System.currentTimeMillis();

        return CompletableFuture.supplyAsync(() -> {
            try {
                if (spec.delayBeforeSec() > 0) {
                    LOG.info("Waiting " + spec.delayBeforeSec() + "s before triggering chaos...");
                    TimeUnit.SECONDS.sleep(spec.delayBeforeSec());
                }

                long chaosStartNanos = System.nanoTime();
                Instant chaosStart = Instant.now();
                createChaosEngine(engineName, spec);
                LOG.info("Created ChaosEngine: " + engineName);

                waitForCompletion(engineName, spec);
                Instant chaosEnd = Instant.now();

                String verdict = readVerdict(engineName, spec.targetNamespace());
                LOG.info("Chaos experiment " + engineName + " completed: " + verdict);

                if ("Pass".equalsIgnoreCase(verdict)) {
                    return ChaosOutcome.success(engineName, spec.experimentName(),
                            chaosStart, chaosEnd, chaosStartNanos);
                } else {
                    return ChaosOutcome.failure(engineName, spec.experimentName(),
                            chaosStart, chaosEnd, chaosStartNanos, verdict);
                }
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                return ChaosOutcome.failure(engineName, spec.experimentName(),
                        Instant.now(), Instant.now(), System.nanoTime(), "Interrupted");
            } catch (Exception e) {
                LOG.log(Level.SEVERE, "Chaos experiment failed: " + engineName, e);
                return ChaosOutcome.failure(engineName, spec.experimentName(),
                        Instant.now(), Instant.now(), System.nanoTime(), e.getMessage());
            }
        });
    }

    @Override
    public ChaosStatus pollStatus(String engineName) {
        try {
            GenericKubernetesResourceList resultList = client
                    .genericKubernetesResources(CHAOS_RESULT_CONTEXT)
                    .inNamespace("kafka")
                    .list();

            for (GenericKubernetesResource result : resultList.getItems()) {
                if (result.getMetadata().getName().startsWith(engineName)) {
                    Object statusObj = result.getAdditionalProperties().get("status");
                    if (statusObj instanceof Map<?, ?> status) {
                        Object expStatus = status.get("experimentStatus");
                        String phase = String.valueOf(expStatus);
                        if (phase.contains("Completed")) return ChaosStatus.COMPLETED;
                        if (phase.contains("Running")) return ChaosStatus.RUNNING;
                        if (phase.contains("Error")) return ChaosStatus.FAILED;
                    }
                    return ChaosStatus.PENDING;
                }
            }
            return ChaosStatus.NOT_FOUND;
        } catch (Exception e) {
            LOG.log(Level.WARNING, "Failed to poll chaos status for " + engineName, e);
            return ChaosStatus.NOT_FOUND;
        }
    }

    @Override
    public void cleanup(String engineName) {
        try {
            client.genericKubernetesResources(CHAOS_ENGINE_CONTEXT)
                    .inNamespace("kafka")
                    .withName(engineName)
                    .delete();
            LOG.info("Cleaned up ChaosEngine: " + engineName);
        } catch (Exception e) {
            LOG.log(Level.WARNING, "Failed to cleanup ChaosEngine: " + engineName, e);
        }
    }

    @Override
    public boolean isAvailable() {
        try {
            client.genericKubernetesResources(CHAOS_ENGINE_CONTEXT)
                    .inNamespace("kafka")
                    .list();
            return true;
        } catch (Exception e) {
            LOG.fine("Litmus CRD provider not available: " + e.getMessage());
            return false;
        }
    }

    private void createChaosEngine(String engineName, FaultSpec spec) {
        GenericKubernetesResource engine = new GenericKubernetesResource();
        engine.setApiVersion("litmuschaos.io/v1alpha1");
        engine.setKind("ChaosEngine");
        engine.setMetadata(new ObjectMetaBuilder()
                .withName(engineName)
                .withNamespace(spec.targetNamespace())
                .addToLabels("app.kubernetes.io/managed-by", "kates")
                .build());

        Map<String, Object> specMap = new HashMap<>();
        specMap.put("annotationCheck", "false");
        specMap.put("engineState", "active");
        specMap.put("chaosServiceAccount", "litmus-admin");

        List<Map<String, Object>> envList = new java.util.ArrayList<>();
        envList.add(Map.of("name", "TOTAL_CHAOS_DURATION", "value", String.valueOf(spec.chaosDurationSec())));
        envList.add(Map.of("name", "APP_NS", "value", spec.targetNamespace()));
        envList.add(Map.of("name", "APP_LABEL", "value", spec.targetLabel()));
        envList.add(Map.of("name", "FORCE", "value", "true"));
        if (!spec.targetPod().isEmpty()) {
            envList.add(Map.of("name", "TARGET_PODS", "value", spec.targetPod()));
        }
        spec.envOverrides().forEach((k, v) -> envList.add(Map.of("name", k, "value", v)));

        Map<String, Object> experiment = Map.of(
                "name", spec.experimentName(),
                "spec", Map.of("components", Map.of("env", envList))
        );
        specMap.put("experiments", List.of(experiment));
        engine.setAdditionalProperty("spec", specMap);

        client.genericKubernetesResources(CHAOS_ENGINE_CONTEXT)
                .inNamespace(spec.targetNamespace())
                .resource(engine)
                .create();
    }

    private void waitForCompletion(String engineName, FaultSpec spec) throws InterruptedException {
        int maxWaitSec = spec.chaosDurationSec() + 120;
        int elapsed = 0;
        while (elapsed < maxWaitSec) {
            ChaosStatus status = pollStatus(engineName);
            if (status == ChaosStatus.COMPLETED || status == ChaosStatus.FAILED) {
                return;
            }
            TimeUnit.SECONDS.sleep(5);
            elapsed += 5;
        }
        LOG.warning("Chaos experiment " + engineName + " did not complete within " + maxWaitSec + "s");
    }

    private String readVerdict(String engineName, String namespace) {
        try {
            GenericKubernetesResourceList resultList = client
                    .genericKubernetesResources(CHAOS_RESULT_CONTEXT)
                    .inNamespace(namespace)
                    .list();

            for (GenericKubernetesResource result : resultList.getItems()) {
                if (result.getMetadata().getName().contains(engineName)) {
                    Object status = result.getAdditionalProperties().get("status");
                    if (status instanceof Map<?, ?> statusMap) {
                        Object expStatus = statusMap.get("experimentStatus");
                        if (expStatus instanceof Map<?, ?> expMap) {
                            Object verdict = expMap.get("verdict");
                            return verdict != null ? String.valueOf(verdict) : "Unknown";
                        }
                    }
                }
            }
        } catch (Exception e) {
            LOG.log(Level.WARNING, "Failed to read verdict for " + engineName, e);
        }
        return "Unknown";
    }
}
