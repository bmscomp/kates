package com.klster.kates.chaos;

import io.fabric8.kubernetes.api.model.GenericKubernetesResource;
import io.fabric8.kubernetes.api.model.GenericKubernetesResourceBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.dsl.base.CustomResourceDefinitionContext;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.inject.Named;

import java.time.Instant;
import java.util.*;
import java.util.concurrent.CompletableFuture;
import java.util.logging.Level;
import java.util.logging.Logger;

/**
 * Chaos provider that creates Litmus ChaosEngine CRs to trigger experiments.
 * Requires the LitmusChaos operator to be deployed in the cluster and the
 * corresponding ChaosExperiment templates to exist in the target namespace.
 */
@ApplicationScoped
@Named("litmus-crd")
public class LitmusChaosProvider implements ChaosProvider {

    private static final Logger LOG = Logger.getLogger(LitmusChaosProvider.class.getName());

    private static final CustomResourceDefinitionContext CHAOS_ENGINE_CTX =
            new CustomResourceDefinitionContext.Builder()
                    .withGroup("litmuschaos.io")
                    .withVersion("v1alpha1")
                    .withPlural("chaosengines")
                    .withScope("Namespaced")
                    .build();

    private static final CustomResourceDefinitionContext CHAOS_RESULT_CTX =
            new CustomResourceDefinitionContext.Builder()
                    .withGroup("litmuschaos.io")
                    .withVersion("v1alpha1")
                    .withPlural("chaosresults")
                    .withScope("Namespaced")
                    .build();

    private static final Map<DisruptionType, String> EXPERIMENT_MAP = Map.of(
            DisruptionType.POD_KILL, "pod-delete",
            DisruptionType.POD_DELETE, "pod-delete",
            DisruptionType.CPU_STRESS, "pod-cpu-hog",
            DisruptionType.DISK_FILL, "disk-fill",
            DisruptionType.NETWORK_PARTITION, "pod-network-partition",
            DisruptionType.NETWORK_LATENCY, "pod-network-latency"
    );

    @Inject
    KubernetesClient client;

    @Override
    public String name() {
        return "litmus-crd";
    }

    @Override
    public CompletableFuture<ChaosOutcome> triggerFault(FaultSpec spec) {
        return CompletableFuture.supplyAsync(() -> {
            Instant start = Instant.now();
            long startNanos = System.nanoTime();
            String engineName = "kates-" + spec.experimentName() + "-" + System.currentTimeMillis();

            try {
                String experimentName = resolveExperiment(spec);

                Map<String, Object> engineSpec = buildChaosEngineSpec(spec, experimentName);

                GenericKubernetesResource engine = new GenericKubernetesResourceBuilder()
                        .withApiVersion("litmuschaos.io/v1alpha1")
                        .withKind("ChaosEngine")
                        .withNewMetadata()
                            .withName(engineName)
                            .withNamespace(spec.targetNamespace())
                            .addToLabels("managed-by", "kates")
                        .endMetadata()
                        .build();
                engine.setAdditionalProperties(Map.of("spec", engineSpec));

                client.genericKubernetesResources(CHAOS_ENGINE_CTX)
                        .inNamespace(spec.targetNamespace())
                        .resource(engine)
                        .create();

                LOG.info("Created ChaosEngine: " + engineName);

                int timeoutSec = spec.chaosDurationSec() + 120;
                String verdict = pollForVerdict(spec.targetNamespace(), engineName, timeoutSec);

                if ("Pass".equalsIgnoreCase(verdict)) {
                    return ChaosOutcome.success(engineName, experimentName,
                            start, Instant.now(), startNanos);
                } else {
                    return ChaosOutcome.failure(engineName, experimentName,
                            start, Instant.now(), startNanos,
                            "ChaosResult verdict: " + verdict);
                }

            } catch (Exception e) {
                LOG.log(Level.SEVERE, "Litmus fault injection failed", e);
                return ChaosOutcome.failure(engineName, spec.experimentName(),
                        start, Instant.now(), startNanos, e.getMessage());
            }
        });
    }

    @SuppressWarnings("unchecked")
    private String pollForVerdict(String namespace, String engineName, int timeoutSec)
            throws InterruptedException {
        long deadline = System.currentTimeMillis() + timeoutSec * 1000L;

        while (System.currentTimeMillis() < deadline) {
            try {
                var results = client.genericKubernetesResources(CHAOS_RESULT_CTX)
                        .inNamespace(namespace)
                        .withLabel("chaosUID")
                        .list()
                        .getItems();

                for (var result : results) {
                    String name = result.getMetadata().getName();
                    if (name.contains(engineName)) {
                        Map<String, Object> status = (Map<String, Object>)
                                result.getAdditionalProperties().get("status");
                        if (status != null) {
                            Map<String, Object> expStatus = (Map<String, Object>)
                                    status.get("experimentStatus");
                            if (expStatus != null) {
                                String verdict = (String) expStatus.get("verdict");
                                if (verdict != null && !verdict.equalsIgnoreCase("Awaited")) {
                                    return verdict;
                                }
                            }
                        }
                    }
                }
            } catch (Exception e) {
                LOG.log(Level.FINE, "Error polling ChaosResult", e);
            }
            Thread.sleep(5000);
        }
        return "Timeout";
    }

    private String resolveExperiment(FaultSpec spec) {
        if (spec.disruptionType() != null && EXPERIMENT_MAP.containsKey(spec.disruptionType())) {
            return EXPERIMENT_MAP.get(spec.disruptionType());
        }
        return spec.experimentName();
    }

    private Map<String, Object> buildChaosEngineSpec(FaultSpec spec, String experimentName) {
        Map<String, Object> engineSpec = new LinkedHashMap<>();
        engineSpec.put("engineState", "active");
        engineSpec.put("chaosServiceAccount", "litmus-admin");
        engineSpec.put("annotationCheck", "false");

        Map<String, Object> appinfo = new LinkedHashMap<>();
        appinfo.put("appns", spec.targetNamespace());
        appinfo.put("applabel", spec.targetLabel());
        appinfo.put("appkind", "statefulset");
        engineSpec.put("appinfo", appinfo);

        Map<String, Object> experiment = new LinkedHashMap<>();
        experiment.put("name", experimentName);

        Map<String, Object> component = new LinkedHashMap<>();
        List<Map<String, String>> envVars = new ArrayList<>();
        envVars.add(Map.of("name", "TOTAL_CHAOS_DURATION", "value", String.valueOf(spec.chaosDurationSec())));

        if (!spec.targetPod().isEmpty()) {
            envVars.add(Map.of("name", "TARGET_PODS", "value", spec.targetPod()));
        }

        for (Map.Entry<String, String> e : spec.envOverrides().entrySet()) {
            envVars.add(Map.of("name", e.getKey(), "value", e.getValue()));
        }

        component.put("env", envVars);
        experiment.put("spec", Map.of("components", component));

        engineSpec.put("experiments", List.of(experiment));
        return engineSpec;
    }

    @Override
    public ChaosStatus pollStatus(String engineName) {
        try {
            GenericKubernetesResource engine = client.genericKubernetesResources(CHAOS_ENGINE_CTX)
                    .inAnyNamespace()
                    .withLabel("managed-by", "kates")
                    .list()
                    .getItems().stream()
                    .filter(e -> e.getMetadata().getName().equals(engineName))
                    .findFirst()
                    .orElse(null);

            if (engine == null) return ChaosStatus.NOT_FOUND;

            @SuppressWarnings("unchecked")
            Map<String, Object> status = (Map<String, Object>)
                    engine.getAdditionalProperties().get("status");
            if (status == null) return ChaosStatus.PENDING;

            String engineStatus = (String) status.getOrDefault("engineStatus", "");
            return switch (engineStatus.toLowerCase()) {
                case "completed" -> ChaosStatus.COMPLETED;
                case "stopped" -> ChaosStatus.COMPLETED;
                default -> ChaosStatus.RUNNING;
            };
        } catch (Exception e) {
            return ChaosStatus.NOT_FOUND;
        }
    }

    @Override
    public void cleanup(String engineName) {
        try {
            client.genericKubernetesResources(CHAOS_ENGINE_CTX)
                    .inAnyNamespace()
                    .withLabel("managed-by", "kates")
                    .delete();
            LOG.info("Cleaned up KATES-managed ChaosEngines");
        } catch (Exception e) {
            LOG.log(Level.WARNING, "ChaosEngine cleanup failed", e);
        }
    }

    @Override
    public boolean isAvailable() {
        try {
            client.genericKubernetesResources(CHAOS_ENGINE_CTX)
                    .inAnyNamespace()
                    .list();
            return true;
        } catch (Exception e) {
            return false;
        }
    }
}
