package com.bmscomp.kates.service;

import java.util.ArrayList;
import java.util.Collection;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.TimeUnit;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.DescribeClusterResult;
import org.apache.kafka.common.Node;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import io.fabric8.kubernetes.api.model.GenericKubernetesResource;
import io.fabric8.kubernetes.api.model.Pod;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.dsl.base.CustomResourceDefinitionContext;

@ApplicationScoped
public class ClusterTopologyService {

    private static final Logger LOG = Logger.getLogger(ClusterTopologyService.class);
    private static final int TIMEOUT_SECONDS = 30;

    private static final CustomResourceDefinitionContext NODE_POOL_CRD = new CustomResourceDefinitionContext.Builder()
            .withGroup("kafka.strimzi.io")
            .withVersion("v1")
            .withPlural("kafkanodepools")
            .withScope("Namespaced")
            .build();

    private static final CustomResourceDefinitionContext KAFKA_CRD = new CustomResourceDefinitionContext.Builder()
            .withGroup("kafka.strimzi.io")
            .withVersion("v1")
            .withPlural("kafkas")
            .withScope("Namespaced")
            .build();

    @Inject
    KubernetesClient kubernetesClient;

    @Inject
    KafkaAdminService adminService;

    @ConfigProperty(name = "kates.topology.kafka-namespace", defaultValue = "kafka")
    String kafkaNamespace;

    @ConfigProperty(name = "kates.topology.kafka-cluster", defaultValue = "krafter")
    String kafkaCluster;

    @SuppressWarnings("unchecked")
    public Map<String, Object> describeTopology() {
        try {
            kubernetesClient.pods().inNamespace(kafkaNamespace).list();
        } catch (Exception e) {
            throw new KubernetesNotAvailableException(
                    "Cluster topology requires the backend to be running on Kubernetes "
                            + "with access to Strimzi CRDs. Kubernetes API is not available: " + e.getMessage());
        }

        Map<String, Object> topology = new LinkedHashMap<>();
        topology.put("clusterName", kafkaCluster);
        topology.put("kraftMode", true);

        String kafkaVersion = readKafkaVersion();
        topology.put("kafkaVersion", kafkaVersion != null ? kafkaVersion : "unknown");

        int controllerQuorumLeader = -1;
        Map<Integer, Map<String, Object>> brokerNodes = new LinkedHashMap<>();
        try {
            AdminClient client = adminService.getClient();
            DescribeClusterResult cluster = client.describeCluster();
            Node controller = cluster.controller().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            controllerQuorumLeader = controller.id();

            Collection<Node> nodes = cluster.nodes().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            for (Node node : nodes) {
                Map<String, Object> n = new LinkedHashMap<>();
                n.put("id", node.id());
                n.put("host", node.host());
                n.put("port", node.port());
                n.put("rack", node.rack() != null ? node.rack() : "");
                brokerNodes.put(node.id(), n);
            }
        } catch (Exception e) {
            LOG.warn("Failed to query Kafka AdminClient for topology", e);
        }
        topology.put("controllerQuorumLeader", controllerQuorumLeader);

        List<Map<String, Object>> nodePools = new ArrayList<>();
        List<Map<String, Object>> allNodes = new ArrayList<>();

        try {
            var pools = kubernetesClient.genericKubernetesResources(NODE_POOL_CRD)
                    .inNamespace(kafkaNamespace)
                    .withLabel("strimzi.io/cluster", kafkaCluster)
                    .list()
                    .getItems();

            for (GenericKubernetesResource pool : pools) {
                Map<String, Object> props = pool.getAdditionalProperties();
                Map<String, Object> spec = (Map<String, Object>) props.getOrDefault("spec", Map.of());

                String poolName = pool.getMetadata().getName();
                List<String> roles = (List<String>) spec.getOrDefault("roles", List.of());
                String role = roles.isEmpty() ? "unknown" : roles.get(0);
                int replicas = spec.get("replicas") instanceof Number n ? n.intValue() : 0;

                Map<String, Object> storage = (Map<String, Object>) spec.getOrDefault("storage", Map.of());
                String storageType = (String) storage.getOrDefault("type", "unknown");
                String storageSize = "";
                if (storage.get("volumes") instanceof List<?> volumes && !volumes.isEmpty()) {
                    Map<String, Object> vol = (Map<String, Object>) volumes.get(0);
                    storageSize = (String) vol.getOrDefault("size", "");
                }

                Map<String, Object> poolInfo = new LinkedHashMap<>();
                poolInfo.put("name", poolName);
                poolInfo.put("role", role);
                poolInfo.put("replicas", replicas);
                poolInfo.put("storageType", storageType);
                poolInfo.put("storageSize", storageSize);
                nodePools.add(poolInfo);

                List<Pod> pods = kubernetesClient.pods()
                        .inNamespace(kafkaNamespace)
                        .withLabel("strimzi.io/pool-name", poolName)
                        .withLabel("strimzi.io/cluster", kafkaCluster)
                        .list()
                        .getItems();

                for (Pod pod : pods) {
                    Map<String, Object> nodeInfo = new LinkedHashMap<>();
                    String podName = pod.getMetadata().getName();
                    int nodeId = extractNodeId(podName);

                    if ("broker".equals(role) && brokerNodes.containsKey(nodeId)) {
                        Map<String, Object> bn = brokerNodes.get(nodeId);
                        nodeInfo.put("id", nodeId);
                        nodeInfo.put("host", bn.get("host"));
                        nodeInfo.put("port", bn.get("port"));
                        nodeInfo.put("rack", bn.get("rack"));
                    } else {
                        nodeInfo.put("id", nodeId);
                        nodeInfo.put("host",
                                podName + "." + kafkaCluster + "-kafka-brokers." + kafkaNamespace + ".svc");
                        nodeInfo.put("port", 9092);
                        nodeInfo.put("rack", pod.getMetadata().getLabels().getOrDefault("zone", ""));
                    }

                    nodeInfo.put("role", role);
                    nodeInfo.put("pool", poolName);
                    nodeInfo.put("status", isPodReady(pod) ? "Ready" : "NotReady");
                    nodeInfo.put("isQuorumLeader", nodeId == controllerQuorumLeader);
                    allNodes.add(nodeInfo);
                }
            }
        } catch (KubernetesNotAvailableException e) {
            throw e;
        } catch (Exception e) {
            LOG.warn("Failed to read Strimzi KafkaNodePool CRDs", e);
        }

        nodePools.sort(Comparator.comparing(p -> (String) p.get("name")));
        allNodes.sort(Comparator.comparingInt(n -> (int) n.get("id")));

        topology.put("nodePools", nodePools);
        topology.put("nodes", allNodes);
        return topology;
    }

    @SuppressWarnings("unchecked")
    private String readKafkaVersion() {
        try {
            GenericKubernetesResource kafka = kubernetesClient.genericKubernetesResources(KAFKA_CRD)
                    .inNamespace(kafkaNamespace)
                    .withName(kafkaCluster)
                    .get();
            if (kafka != null) {
                Map<String, Object> spec = (Map<String, Object>) kafka.getAdditionalProperties().get("spec");
                if (spec != null) {
                    Map<String, Object> kafkaSpec = (Map<String, Object>) spec.get("kafka");
                    if (kafkaSpec != null && kafkaSpec.get("version") != null) {
                        return kafkaSpec.get("version").toString();
                    }
                }
            }
        } catch (Exception e) {
            LOG.debug("Unable to read Kafka version from CR", e);
        }
        return null;
    }

    private int extractNodeId(String podName) {
        String[] parts = podName.split("-");
        try {
            return Integer.parseInt(parts[parts.length - 1]);
        } catch (NumberFormatException e) {
            return -1;
        }
    }

    private boolean isPodReady(Pod pod) {
        if (pod.getStatus() == null || pod.getStatus().getConditions() == null) {
            return false;
        }
        return pod.getStatus().getConditions().stream()
                .filter(c -> "Ready".equals(c.getType()))
                .anyMatch(c -> "True".equals(c.getStatus()));
    }

    public static class KubernetesNotAvailableException extends RuntimeException {
        public KubernetesNotAvailableException(String message) {
            super(message);
        }
    }
}
