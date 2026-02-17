package com.klster.kates.chaos;

import io.fabric8.kubernetes.api.model.GenericKubernetesResource;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.dsl.base.CustomResourceDefinitionContext;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import java.time.Duration;
import java.time.Instant;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import org.jboss.logging.Logger;

/**
 * Monitors the Strimzi {@code Kafka} Custom Resource status to determine
 * cluster-level health and recovery timing after disruptions.
 */
@ApplicationScoped
public class StrimziStateTracker {

    private static final Logger LOG = Logger.getLogger(StrimziStateTracker.class);

    private static final CustomResourceDefinitionContext KAFKA_CRD = new CustomResourceDefinitionContext.Builder()
            .withGroup("kafka.strimzi.io")
            .withVersion("v1beta2")
            .withPlural("kafkas")
            .withScope("Namespaced")
            .build();

    @Inject
    KubernetesClient client;

    public record ReplicationHealth(
            int totalPartitions,
            int underReplicatedPartitions,
            int offlinePartitions,
            boolean healthy
    ) {}

    /**
     * Polls the Strimzi Kafka CR until it returns to {@code Ready} status
     * after a disruption, or until the timeout expires.
     *
     * @return the duration from disruption start until Kafka CR reports Ready,
     *         or the timeout duration if recovery did not complete.
     */
    public Duration measureRecoveryTime(String namespace, String kafkaCluster,
                                         Instant disruptionStart, Duration timeout) {
        Instant deadline = Instant.now().plus(timeout);

        while (Instant.now().isBefore(deadline)) {
            try {
                if (isKafkaReady(namespace, kafkaCluster)) {
                    Duration recovery = Duration.between(disruptionStart, Instant.now());
                    LOG.info("Kafka CR '" + kafkaCluster + "' recovered in " + recovery.toSeconds() + "s");
                    return recovery;
                }
                Thread.sleep(2000);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                break;
            } catch (Exception e) {
                LOG.debug("Error polling Kafka CR status", e);
                try { Thread.sleep(5000); } catch (InterruptedException ie) {
                    Thread.currentThread().interrupt();
                    break;
                }
            }
        }

        Duration elapsed = Duration.between(disruptionStart, Instant.now());
        LOG.warn("Kafka CR '" + kafkaCluster + "' did not recover within timeout ("
                + timeout.toSeconds() + "s), elapsed: " + elapsed.toSeconds() + "s");
        return elapsed;
    }

    @SuppressWarnings("unchecked")
    boolean isKafkaReady(String namespace, String kafkaCluster) {
        try {
            GenericKubernetesResource kafka = client.genericKubernetesResources(KAFKA_CRD)
                    .inNamespace(namespace)
                    .withName(kafkaCluster)
                    .get();

            if (kafka == null) {
                return false;
            }

            Map<String, Object> status = (Map<String, Object>) kafka.getAdditionalProperties().get("status");
            if (status == null) {
                return false;
            }

            List<Map<String, Object>> conditions = (List<Map<String, Object>>) status.getOrDefault(
                    "conditions", Collections.emptyList());

            return conditions.stream()
                    .filter(c -> "Ready".equals(c.get("type")))
                    .anyMatch(c -> "True".equals(c.get("status")));
        } catch (Exception e) {
            LOG.debug("Failed to read Kafka CR status", e);
            return false;
        }
    }
}
