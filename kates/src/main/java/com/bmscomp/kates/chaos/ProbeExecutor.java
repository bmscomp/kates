package com.bmscomp.kates.chaos;

import java.io.ByteArrayOutputStream;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.TimeUnit;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.fabric8.kubernetes.client.KubernetesClient;
import org.jboss.logging.Logger;

/**
 * Evaluates {@link ProbeSpec} probes by executing commands against cluster pods.
 * Uses Fabric8 exec() for cmdProbe probes and kubectl-style queries for k8sProbe probes.
 */
@ApplicationScoped
public class ProbeExecutor {

    private static final Logger LOG = Logger.getLogger(ProbeExecutor.class);

    @Inject
    KubernetesClient client;

    /**
     * Evaluate a single probe against the cluster.
     */
    public ProbeResult evaluate(ProbeSpec probe, String namespace) {
        long startNanos = System.nanoTime();
        try {
            String output = executeProbe(probe, namespace);
            boolean passed = checkComparator(output, probe.expectedOutput(), probe.comparator());
            long durationMs = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - startNanos);

            if (passed) {
                LOG.debugf("Probe '%s' PASSED (%dms)", probe.name(), durationMs);
                return ProbeResult.pass(probe.name(), output, durationMs);
            } else {
                LOG.infof("Probe '%s' FAILED: expected '%s' (%s) in output", probe.name(), probe.expectedOutput(), probe.comparator());
                return ProbeResult.fail(probe.name(), output, durationMs);
            }
        } catch (Exception e) {
            long durationMs = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - startNanos);
            LOG.warnf("Probe '%s' ERROR: %s", probe.name(), e.getMessage());
            return ProbeResult.fail(probe.name(), "Error: " + e.getMessage(), durationMs);
        }
    }

    /**
     * Evaluate all probes and return results.
     */
    public List<ProbeResult> evaluateAll(List<ProbeSpec> probes, String namespace) {
        List<ProbeResult> results = new ArrayList<>();
        for (ProbeSpec probe : probes) {
            results.add(evaluate(probe, namespace));
        }
        return results;
    }

    private String executeProbe(ProbeSpec probe, String namespace) {
        if ("k8sProbe".equals(probe.type())) {
            return executeK8sProbe(probe, namespace);
        }
        return executeCmdProbe(probe, namespace);
    }

    private String executeCmdProbe(ProbeSpec probe, String namespace) {
        var pods = client.pods()
                .inNamespace(namespace)
                .withLabel("strimzi.io/component-type", "kafka")
                .list()
                .getItems();

        if (pods.isEmpty()) {
            return "No Kafka pods found in namespace " + namespace;
        }

        String targetPod = pods.getFirst().getMetadata().getName();
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        var latch = new java.util.concurrent.CountDownLatch(1);

        try (var watch = client.pods()
                .inNamespace(namespace)
                .withName(targetPod)
                .writingOutput(out)
                .usingListener(new io.fabric8.kubernetes.client.dsl.ExecListener() {
                    @Override public void onClose(int code, String reason) { latch.countDown(); }
                    @Override public void onFailure(Throwable t, io.fabric8.kubernetes.client.dsl.ExecListener.Response resp) { latch.countDown(); }
                })
                .exec("sh", "-c", probe.command())) {
            latch.await(probe.timeoutSec(), TimeUnit.SECONDS);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            return "Probe interrupted";
        }

        return out.toString().trim();
    }

    private String executeK8sProbe(ProbeSpec probe, String namespace) {
        String command = probe.command();
        if (command.contains("kafka") && command.contains("Ready")) {
            try {
                var kafkas = client.genericKubernetesResources(
                                "kafka.strimzi.io/v1beta2", "Kafka")
                        .inNamespace(namespace)
                        .list();

                if (kafkas.getItems().isEmpty()) {
                    return "No Kafka CR found";
                }

                var kafka = kafkas.getItems().getFirst();
                var status = kafka.getAdditionalProperties().get("status");
                return status != null ? status.toString() : "No status";
            } catch (Exception e) {
                return "K8s probe failed: " + e.getMessage();
            }
        }

        return executeCmdProbe(probe, namespace);
    }

    private boolean checkComparator(String actual, String expected, String comparator) {
        if (actual == null || expected == null) return false;
        return switch (comparator != null ? comparator : "contains") {
            case "equal" -> actual.trim().equals(expected.trim());
            case "contains" -> actual.contains(expected);
            case "notContains" -> !actual.contains(expected);
            case ">=" -> parseDouble(actual) >= parseDouble(expected);
            case "<=" -> parseDouble(actual) <= parseDouble(expected);
            case ">" -> parseDouble(actual) > parseDouble(expected);
            case "<" -> parseDouble(actual) < parseDouble(expected);
            default -> actual.contains(expected);
        };
    }

    private double parseDouble(String s) {
        try {
            String cleaned = s.replaceAll("[^0-9.\\-]", "").trim();
            return Double.parseDouble(cleaned);
        } catch (NumberFormatException e) {
            return 0;
        }
    }
}
