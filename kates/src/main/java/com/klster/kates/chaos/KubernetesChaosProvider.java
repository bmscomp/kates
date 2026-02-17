package com.klster.kates.chaos;

import io.fabric8.kubernetes.api.model.networking.v1.*;
import io.fabric8.kubernetes.client.KubernetesClient;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.inject.Named;

import java.time.Instant;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import org.jboss.logging.Logger;

/**
 * Chaos provider using direct Kubernetes API calls.
 * Supports pod deletion, network policy injection, and StatefulSet manipulation
 * without requiring external chaos infrastructure like Litmus.
 */
@ApplicationScoped
@Named("kubernetes")
public class KubernetesChaosProvider implements ChaosProvider {

    private static final Logger LOG = Logger.getLogger(KubernetesChaosProvider.class);

    @Inject
    KubernetesClient client;

    @Override
    public String name() {
        return "kubernetes";
    }

    @Override
    public CompletableFuture<ChaosOutcome> triggerFault(FaultSpec spec) {
        return CompletableFuture.supplyAsync(() -> {
            Instant start = Instant.now();
            long startNanos = System.nanoTime();
            String engineName = spec.experimentName() + "-" + System.currentTimeMillis();

            try {
                if (spec.disruptionType() == null) {
                    return ChaosOutcome.skipped("No disruptionType set — use the builder");
                }

                if (spec.delayBeforeSec() > 0) {
                    Thread.sleep(spec.delayBeforeSec() * 1000L);
                }

                switch (spec.disruptionType()) {
                    case POD_KILL -> executePodKill(spec);
                    case POD_DELETE -> executePodDelete(spec);
                    case NETWORK_PARTITION -> executeNetworkPartition(spec);
                    case ROLLING_RESTART -> executeRollingRestart(spec);
                    case SCALE_DOWN -> executeScaleDown(spec);
                    case LEADER_ELECTION -> executePodKill(spec);
                    default -> {
                        return ChaosOutcome.skipped(
                                "DisruptionType " + spec.disruptionType()
                                        + " not supported by kubernetes provider");
                    }
                }

                if (spec.chaosDurationSec() > 0
                        && spec.disruptionType() == DisruptionType.NETWORK_PARTITION) {
                    Thread.sleep(spec.chaosDurationSec() * 1000L);
                    cleanup(engineName);
                }

                return ChaosOutcome.success(engineName, spec.experimentName(),
                        start, Instant.now(), startNanos);

            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                return ChaosOutcome.failure(engineName, spec.experimentName(),
                        start, Instant.now(), startNanos, "Interrupted");
            } catch (Exception e) {
                LOG.error("Fault injection failed", e);
                return ChaosOutcome.failure(engineName, spec.experimentName(),
                        start, Instant.now(), startNanos, e.getMessage());
            }
        });
    }

    private void executePodKill(FaultSpec spec) {
        String podName = resolvePodName(spec);
        LOG.info("POD_KILL: force-deleting " + podName);
        client.pods()
                .inNamespace(spec.targetNamespace())
                .withName(podName)
                .withGracePeriod(0)
                .delete();
    }

    private void executePodDelete(FaultSpec spec) {
        String podName = resolvePodName(spec);
        LOG.info("POD_DELETE: gracefully deleting " + podName + " (grace=" + spec.gracePeriodSec() + "s)");
        client.pods()
                .inNamespace(spec.targetNamespace())
                .withName(podName)
                .withGracePeriod(spec.gracePeriodSec())
                .delete();
    }

    private void executeNetworkPartition(FaultSpec spec) {
        String podName = resolvePodName(spec);
        String policyName = "kates-netpol-" + podName;

        LOG.info("NETWORK_PARTITION: applying deny NetworkPolicy for " + podName);

        var pod = client.pods()
                .inNamespace(spec.targetNamespace())
                .withName(podName)
                .get();
        if (pod == null) {
            throw new IllegalStateException("Pod not found: " + podName);
        }

        Map<String, String> podLabels = pod.getMetadata().getLabels();

        NetworkPolicy policy = new NetworkPolicyBuilder()
                .withNewMetadata()
                    .withName(policyName)
                    .withNamespace(spec.targetNamespace())
                    .addToLabels("managed-by", "kates")
                .endMetadata()
                .withNewSpec()
                    .withNewPodSelector()
                        .addToMatchLabels(podLabels)
                    .endPodSelector()
                    .withPolicyTypes("Ingress", "Egress")
                .endSpec()
                .build();

        client.network().networkPolicies()
                .inNamespace(spec.targetNamespace())
                .resource(policy)
                .create();
    }

    private void executeRollingRestart(FaultSpec spec) {
        String[] parts = spec.targetLabel().split("=", 2);
        String labelKey = parts[0];
        String labelValue = parts.length > 1 ? parts[1] : "";

        LOG.info("ROLLING_RESTART: restarting StatefulSets with label " + spec.targetLabel());

        client.apps().statefulSets()
                .inNamespace(spec.targetNamespace())
                .withLabel(labelKey, labelValue)
                .list()
                .getItems()
                .forEach(ss -> {
                    LOG.info("Rolling restart: " + ss.getMetadata().getName());
                    client.apps().statefulSets()
                            .inNamespace(spec.targetNamespace())
                            .withName(ss.getMetadata().getName())
                            .rolling()
                            .restart();
                });
    }

    private void executeScaleDown(FaultSpec spec) {
        String[] parts = spec.targetLabel().split("=", 2);
        String labelKey = parts[0];
        String labelValue = parts.length > 1 ? parts[1] : "";

        client.apps().statefulSets()
                .inNamespace(spec.targetNamespace())
                .withLabel(labelKey, labelValue)
                .list()
                .getItems()
                .forEach(ss -> {
                    int current = ss.getSpec().getReplicas();
                    int target = Math.max(1, current - 1);
                    LOG.info("SCALE_DOWN: " + ss.getMetadata().getName()
                            + " from " + current + " → " + target);
                    client.apps().statefulSets()
                            .inNamespace(spec.targetNamespace())
                            .withName(ss.getMetadata().getName())
                            .scale(target);
                });
    }

    private String resolvePodName(FaultSpec spec) {
        if (spec.targetPod() != null && !spec.targetPod().isEmpty()) {
            return spec.targetPod();
        }

        String[] parts = spec.targetLabel().split("=", 2);
        String labelKey = parts[0];
        String labelValue = parts.length > 1 ? parts[1] : "";

        var pods = client.pods()
                .inNamespace(spec.targetNamespace())
                .withLabel(labelKey, labelValue)
                .list()
                .getItems();

        if (pods.isEmpty()) {
            throw new IllegalStateException("No pods found matching label " + spec.targetLabel());
        }

        if (spec.targetBrokerId() >= 0) {
            return pods.stream()
                    .filter(p -> p.getMetadata().getName().endsWith("-" + spec.targetBrokerId()))
                    .findFirst()
                    .orElse(pods.getFirst())
                    .getMetadata().getName();
        }

        int index = (int) (Math.random() * pods.size());
        return pods.get(index).getMetadata().getName();
    }

    @Override
    public ChaosStatus pollStatus(String engineName) {
        return ChaosStatus.COMPLETED;
    }

    @Override
    public void cleanup(String engineName) {
        try {
            client.network().networkPolicies()
                    .inAnyNamespace()
                    .withLabel("managed-by", "kates")
                    .delete();
            LOG.info("Cleaned up Kates-managed NetworkPolicies");
        } catch (Exception e) {
            LOG.warn("Cleanup failed", e);
        }
    }

    @Override
    public boolean isAvailable() {
        try {
            client.pods().inNamespace("default").list();
            return true;
        } catch (Exception e) {
            return false;
        }
    }
}
