package com.bmscomp.kates.service;

import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.dsl.base.CustomResourceDefinitionContext;

@ApplicationScoped
public class ClusterAlertsService {

    private static final Logger LOG = Logger.getLogger(ClusterAlertsService.class);

    private static final CustomResourceDefinitionContext PROMETHEUS_RULE_CRD = new CustomResourceDefinitionContext.Builder()
            .withGroup("monitoring.coreos.com")
            .withVersion("v1")
            .withPlural("prometheusrules")
            .withScope("Namespaced")
            .build();

    private static final Set<String> CRITICAL_SEVERITIES = Set.of("critical", "warning");

    private static final Set<String> KAFKA_HEALTH_ALERTS = Set.of(
            "KafkaOfflinePartitions",
            "KafkaUnderReplicatedPartitions",
            "KafkaActiveControllerCount",
            "KafkaBrokerDiskUsageHigh",
            "KafkaBrokerDiskUsageCritical",
            "KafkaConsumerGroupLagCritical",
            "KafkaRaftLeaderElectionRate",
            "KafkaRaftUncommittedRecords",
            "KafkaRequestLatencyHigh",
            "StrimziOperatorDown",
            "KafkaISRShrinkRate",
            "KafkaLogFlushLatencyHigh",
            "KafkaRequestHandlerSaturated",
            "CruiseControlAnomalyDetected",
            "KafkaCertificateExpiringSoon",
            "KafkaCertificateExpiryCritical"
    );

    @Inject
    KubernetesClient kubernetesClient;

    @ConfigProperty(name = "kates.topology.kafka-namespace", defaultValue = "kafka")
    String kafkaNamespace;

    @SuppressWarnings("unchecked")
    public Map<String, Object> describeAlerts() {
        Map<String, Object> result = new LinkedHashMap<>();
        List<Map<String, Object>> alerts = new ArrayList<>();
        int totalRules = 0;

        try {
            var rules = kubernetesClient.genericKubernetesResources(PROMETHEUS_RULE_CRD)
                    .inNamespace(kafkaNamespace)
                    .list()
                    .getItems();

            for (var rule : rules) {
                String ruleName = rule.getMetadata().getName();
                Map<String, Object> spec = (Map<String, Object>) rule.getAdditionalProperties()
                        .getOrDefault("spec", Map.of());
                List<Map<String, Object>> groups = (List<Map<String, Object>>) spec.getOrDefault("groups", List.of());

                for (Map<String, Object> group : groups) {
                    String groupName = (String) group.getOrDefault("name", "unknown");
                    List<Map<String, Object>> groupRules = (List<Map<String, Object>>) group.getOrDefault("rules",
                            List.of());
                    totalRules += groupRules.size();

                    for (Map<String, Object> alertRule : groupRules) {
                        String alertName = (String) alertRule.get("alert");
                        if (alertName == null) continue;

                        Map<String, Object> labels = (Map<String, Object>) alertRule.getOrDefault("labels", Map.of());
                        String severity = (String) labels.getOrDefault("severity", "unknown");

                        if (!CRITICAL_SEVERITIES.contains(severity)) continue;
                        if (!KAFKA_HEALTH_ALERTS.contains(alertName)) continue;

                        Map<String, Object> annotations = (Map<String, Object>) alertRule.getOrDefault("annotations",
                                Map.of());

                        Map<String, Object> alert = new LinkedHashMap<>();
                        alert.put("name", alertName);
                        alert.put("severity", severity);
                        alert.put("group", groupName);
                        alert.put("source", ruleName);
                        alert.put("expr", alertRule.get("expr"));
                        alert.put("for", alertRule.getOrDefault("for", "0s"));
                        alert.put("summary", annotations.getOrDefault("summary", ""));
                        alert.put("description", annotations.getOrDefault("description", ""));
                        alerts.add(alert);
                    }
                }
            }
        } catch (Exception e) {
            LOG.warn("Unable to read PrometheusRules", e);
        }

        alerts.sort(Comparator.comparing((Map<String, Object> a) -> {
            String sev = (String) a.get("severity");
            return "critical".equals(sev) ? 0 : 1;
        }).thenComparing(a -> (String) a.get("name")));

        result.put("totalRulesScanned", totalRules);
        result.put("criticalCount", alerts.stream().filter(a -> "critical".equals(a.get("severity"))).count());
        result.put("warningCount", alerts.stream().filter(a -> "warning".equals(a.get("severity"))).count());
        result.put("count", alerts.size());
        result.put("alerts", alerts);
        return result;
    }
}
