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
import io.fabric8.kubernetes.client.VersionInfo;
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

    private static final CustomResourceDefinitionContext KAFKA_TOPIC_CRD = new CustomResourceDefinitionContext.Builder()
            .withGroup("kafka.strimzi.io")
            .withVersion("v1")
            .withPlural("kafkatopics")
            .withScope("Namespaced")
            .build();

    private static final CustomResourceDefinitionContext KAFKA_USER_CRD = new CustomResourceDefinitionContext.Builder()
            .withGroup("kafka.strimzi.io")
            .withVersion("v1")
            .withPlural("kafkausers")
            .withScope("Namespaced")
            .build();

    private static final CustomResourceDefinitionContext KAFKA_CONNECT_CRD = new CustomResourceDefinitionContext.Builder()
            .withGroup("kafka.strimzi.io")
            .withVersion("v1")
            .withPlural("kafkaconnects")
            .withScope("Namespaced")
            .build();

    private static final CustomResourceDefinitionContext KAFKA_MM2_CRD = new CustomResourceDefinitionContext.Builder()
            .withGroup("kafka.strimzi.io")
            .withVersion("v1")
            .withPlural("kafkamirrormaker2s")
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

        // Kubernetes info
        topology.put("kubernetes", describeKubernetes());

        // Strimzi operator info
        topology.put("strimzi", describeStrimzi());

        // Kafka cluster overview
        topology.put("cluster", describeKafkaCluster());

        // Node pools
        topology.put("nodePools", describeNodePools());

        // Nodes (brokers + controllers)
        topology.put("nodes", describeNodes());

        // Topics summary
        topology.put("topics", describeTopics());

        // Users summary
        topology.put("users", describeUsers());

        // Kafka Connect & MirrorMaker2
        topology.put("connect", describeConnect());
        topology.put("mirrorMaker2", describeMirrorMaker2());

        return topology;
    }

    private Map<String, Object> describeKubernetes() {
        Map<String, Object> k8s = new LinkedHashMap<>();
        try {
            VersionInfo version = kubernetesClient.getKubernetesVersion();
            k8s.put("version", version.getMajor() + "." + version.getMinor());
            k8s.put("platform", version.getPlatform());
            k8s.put("gitVersion", version.getGitVersion());
        } catch (Exception e) {
            LOG.debug("Unable to read Kubernetes version", e);
            k8s.put("version", "unknown");
        }

        try {
            var nodes = kubernetesClient.nodes().list().getItems();
            List<Map<String, Object>> nodeList = new ArrayList<>();
            for (var node : nodes) {
                Map<String, Object> n = new LinkedHashMap<>();
                n.put("name", node.getMetadata().getName());
                var status = node.getStatus();
                if (status != null && status.getNodeInfo() != null) {
                    n.put("os", status.getNodeInfo().getOsImage());
                    n.put("containerRuntime", status.getNodeInfo().getContainerRuntimeVersion());
                    n.put("kubeletVersion", status.getNodeInfo().getKubeletVersion());
                    n.put("arch", status.getNodeInfo().getArchitecture());
                }
                var labels = node.getMetadata().getLabels();
                if (labels != null) {
                    n.put("role", labels.getOrDefault("node-role.kubernetes.io/control-plane", null) != null
                            ? "control-plane" : "worker");
                }
                boolean ready = false;
                if (status != null && status.getConditions() != null) {
                    ready = status.getConditions().stream()
                            .filter(c -> "Ready".equals(c.getType()))
                            .anyMatch(c -> "True".equals(c.getStatus()));
                }
                n.put("ready", ready);
                nodeList.add(n);
            }
            k8s.put("nodes", nodeList);
            k8s.put("nodeCount", nodeList.size());
        } catch (Exception e) {
            LOG.debug("Unable to list Kubernetes nodes", e);
        }

        return k8s;
    }

    private Map<String, Object> describeStrimzi() {
        Map<String, Object> strimzi = new LinkedHashMap<>();
        try {
            var pods = kubernetesClient.pods()
                    .inNamespace(kafkaNamespace)
                    .withLabel("strimzi.io/kind", "cluster-operator")
                    .list().getItems();
            if (!pods.isEmpty()) {
                Pod operatorPod = pods.get(0);
                String image = operatorPod.getSpec().getContainers().get(0).getImage();
                strimzi.put("operatorImage", image);
                // Extract version from image tag (e.g. quay.io/strimzi/operator:0.51.0)
                if (image.contains(":")) {
                    strimzi.put("version", image.substring(image.lastIndexOf(':') + 1));
                }
                strimzi.put("operatorReady", isPodReady(operatorPod));
            }
        } catch (Exception e) {
            LOG.debug("Unable to read Strimzi operator info", e);
        }

        // Entity Operator
        try {
            var entityPods = kubernetesClient.pods()
                    .inNamespace(kafkaNamespace)
                    .withLabel("strimzi.io/name", kafkaCluster + "-entity-operator")
                    .list().getItems();
            if (!entityPods.isEmpty()) {
                strimzi.put("entityOperatorReady", isPodReady(entityPods.get(0)));
            }
        } catch (Exception e) {
            LOG.debug("Unable to read Entity Operator info", e);
        }

        // Cruise Control
        try {
            var ccPods = kubernetesClient.pods()
                    .inNamespace(kafkaNamespace)
                    .withLabel("strimzi.io/name", kafkaCluster + "-cruise-control")
                    .list().getItems();
            if (!ccPods.isEmpty()) {
                strimzi.put("cruiseControlReady", isPodReady(ccPods.get(0)));
            }
        } catch (Exception e) {
            LOG.debug("Unable to read Cruise Control info", e);
        }

        // Kafka Exporter
        try {
            var expPods = kubernetesClient.pods()
                    .inNamespace(kafkaNamespace)
                    .withLabel("strimzi.io/name", kafkaCluster + "-kafka-exporter")
                    .list().getItems();
            if (!expPods.isEmpty()) {
                strimzi.put("kafkaExporterReady", isPodReady(expPods.get(0)));
            }
        } catch (Exception e) {
            LOG.debug("Unable to read Kafka Exporter info", e);
        }

        return strimzi;
    }

    @SuppressWarnings("unchecked")
    private Map<String, Object> describeKafkaCluster() {
        Map<String, Object> cluster = new LinkedHashMap<>();
        cluster.put("name", kafkaCluster);
        cluster.put("namespace", kafkaNamespace);
        cluster.put("kraftMode", true);

        // Read from Kafka CR
        String kafkaVersion = "unknown";
        try {
            GenericKubernetesResource kafka = kubernetesClient.genericKubernetesResources(KAFKA_CRD)
                    .inNamespace(kafkaNamespace)
                    .withName(kafkaCluster)
                    .get();
            if (kafka != null) {
                Map<String, Object> props = kafka.getAdditionalProperties();
                Map<String, Object> spec = (Map<String, Object>) props.getOrDefault("spec", Map.of());
                Map<String, Object> kafkaSpec = (Map<String, Object>) spec.getOrDefault("kafka", Map.of());

                if (kafkaSpec.get("version") != null) {
                    kafkaVersion = kafkaSpec.get("version").toString();
                }

                // Listeners
                if (kafkaSpec.get("listeners") instanceof List<?> listeners) {
                    List<Map<String, Object>> listenerList = new ArrayList<>();
                    for (Object l : listeners) {
                        if (l instanceof Map<?, ?> lm) {
                            Map<String, Object> li = new LinkedHashMap<>();
                            li.put("name", lm.get("name"));
                            li.put("type", lm.get("type"));
                            li.put("port", lm.get("port"));
                            li.put("tls", lm.get("tls"));
                            if (lm.get("authentication") instanceof Map<?, ?> auth) {
                                li.put("authType", auth.get("type"));
                            }
                            listenerList.add(li);
                        }
                    }
                    cluster.put("listeners", listenerList);
                }

                // Authorization
                if (kafkaSpec.get("authorization") instanceof Map<?, ?> authz) {
                    Map<String, Object> auth = new LinkedHashMap<>();
                    auth.put("type", authz.get("type"));
                    if (authz.get("superUsers") instanceof List<?> su) {
                        auth.put("superUsers", su);
                    }
                    cluster.put("authorization", auth);
                }

                // Status
                Map<String, Object> status = (Map<String, Object>) props.getOrDefault("status", Map.of());
                if (status.get("conditions") instanceof List<?> conditions) {
                    for (Object c : conditions) {
                        if (c instanceof Map<?, ?> cm && "Ready".equals(cm.get("type"))) {
                            cluster.put("ready", "True".equals(cm.get("status")));
                        }
                    }
                }
            }
        } catch (Exception e) {
            LOG.warn("Failed to read Kafka CR", e);
        }
        cluster.put("kafkaVersion", kafkaVersion);

        // AdminClient cluster info
        int controllerQuorumLeader = -1;
        String clusterId = "unknown";
        int brokerCount = 0;
        try {
            AdminClient client = adminService.getClient();
            DescribeClusterResult result = client.describeCluster();
            clusterId = result.clusterId().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            Node controller = result.controller().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            controllerQuorumLeader = controller.id();
            brokerCount = result.nodes().get(TIMEOUT_SECONDS, TimeUnit.SECONDS).size();
        } catch (Exception e) {
            LOG.warn("Failed to query AdminClient for cluster info", e);
        }
        cluster.put("clusterId", clusterId);
        cluster.put("controllerQuorumLeader", controllerQuorumLeader);
        cluster.put("brokerCount", brokerCount);

        return cluster;
    }

    @SuppressWarnings("unchecked")
    private List<Map<String, Object>> describeNodePools() {
        List<Map<String, Object>> nodePools = new ArrayList<>();
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
                String role = roles.isEmpty() ? "unknown" : String.join(",", roles);
                int replicas = spec.get("replicas") instanceof Number n ? n.intValue() : 0;

                Map<String, Object> storage = (Map<String, Object>) spec.getOrDefault("storage", Map.of());
                String storageType = (String) storage.getOrDefault("type", "unknown");
                String storageSize = "";
                if (storage.get("volumes") instanceof List<?> volumes && !volumes.isEmpty()) {
                    Map<String, Object> vol = (Map<String, Object>) volumes.get(0);
                    storageSize = (String) vol.getOrDefault("size", "");
                }

                // Resources
                Map<String, Object> resources = (Map<String, Object>) spec.getOrDefault("resources", Map.of());

                Map<String, Object> poolInfo = new LinkedHashMap<>();
                poolInfo.put("name", poolName);
                poolInfo.put("role", role);
                poolInfo.put("replicas", replicas);
                poolInfo.put("storageType", storageType);
                poolInfo.put("storageSize", storageSize);
                if (!resources.isEmpty()) {
                    poolInfo.put("resources", resources);
                }
                nodePools.add(poolInfo);
            }
        } catch (Exception e) {
            LOG.warn("Failed to read Strimzi KafkaNodePool CRDs", e);
        }
        nodePools.sort(Comparator.comparing(p -> (String) p.get("name")));
        return nodePools;
    }

    @SuppressWarnings("unchecked")
    private List<Map<String, Object>> describeNodes() {
        List<Map<String, Object>> allNodes = new ArrayList<>();

        Map<Integer, Map<String, Object>> brokerNodes = new LinkedHashMap<>();
        try {
            AdminClient client = adminService.getClient();
            Collection<Node> nodes = client.describeCluster().nodes().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            for (Node node : nodes) {
                Map<String, Object> n = new LinkedHashMap<>();
                n.put("id", node.id());
                n.put("host", node.host());
                n.put("port", node.port());
                n.put("rack", node.rack() != null ? node.rack() : "");
                brokerNodes.put(node.id(), n);
            }
        } catch (Exception e) {
            LOG.warn("Failed to query Kafka AdminClient for nodes", e);
        }

        int controllerQuorumLeader = -1;
        try {
            Node controller = adminService.getClient().describeCluster()
                    .controller().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            controllerQuorumLeader = controller.id();
        } catch (Exception e) {
            LOG.debug("Unable to determine controller leader", e);
        }

        try {
            var pools = kubernetesClient.genericKubernetesResources(NODE_POOL_CRD)
                    .inNamespace(kafkaNamespace)
                    .withLabel("strimzi.io/cluster", kafkaCluster)
                    .list()
                    .getItems();

            for (GenericKubernetesResource pool : pools) {
                Map<String, Object> spec = (Map<String, Object>) pool.getAdditionalProperties()
                        .getOrDefault("spec", Map.of());
                String poolName = pool.getMetadata().getName();
                List<String> roles = (List<String>) spec.getOrDefault("roles", List.of());
                String role = roles.isEmpty() ? "unknown" : roles.get(0);

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

                    // K8s node placement
                    if (pod.getSpec().getNodeName() != null) {
                        nodeInfo.put("k8sNode", pod.getSpec().getNodeName());
                    }

                    allNodes.add(nodeInfo);
                }
            }
        } catch (Exception e) {
            LOG.warn("Failed to read node pool pods", e);
        }

        allNodes.sort(Comparator.comparingInt(n -> (int) n.get("id")));
        return allNodes;
    }

    @SuppressWarnings("unchecked")
    private Map<String, Object> describeTopics() {
        Map<String, Object> topics = new LinkedHashMap<>();
        try {
            var topicList = kubernetesClient.genericKubernetesResources(KAFKA_TOPIC_CRD)
                    .inNamespace(kafkaNamespace)
                    .withLabel("strimzi.io/cluster", kafkaCluster)
                    .list()
                    .getItems();

            topics.put("count", topicList.size());
            List<Map<String, Object>> topicDetails = new ArrayList<>();
            for (GenericKubernetesResource topic : topicList) {
                Map<String, Object> t = new LinkedHashMap<>();
                t.put("name", topic.getMetadata().getName());
                Map<String, Object> spec = (Map<String, Object>) topic.getAdditionalProperties()
                        .getOrDefault("spec", Map.of());
                if (spec.get("partitions") instanceof Number n) t.put("partitions", n.intValue());
                if (spec.get("replicas") instanceof Number n) t.put("replicas", n.intValue());
                topicDetails.add(t);
            }
            topicDetails.sort(Comparator.comparing(t -> (String) t.get("name")));
            topics.put("items", topicDetails);
        } catch (Exception e) {
            LOG.debug("Unable to read KafkaTopics", e);
            topics.put("count", 0);
        }
        return topics;
    }

    @SuppressWarnings("unchecked")
    private Map<String, Object> describeUsers() {
        Map<String, Object> users = new LinkedHashMap<>();
        try {
            var userList = kubernetesClient.genericKubernetesResources(KAFKA_USER_CRD)
                    .inNamespace(kafkaNamespace)
                    .withLabel("strimzi.io/cluster", kafkaCluster)
                    .list()
                    .getItems();

            users.put("count", userList.size());
            List<Map<String, Object>> userDetails = new ArrayList<>();
            for (GenericKubernetesResource user : userList) {
                Map<String, Object> u = new LinkedHashMap<>();
                u.put("name", user.getMetadata().getName());
                Map<String, Object> spec = (Map<String, Object>) user.getAdditionalProperties()
                        .getOrDefault("spec", Map.of());
                if (spec.get("authentication") instanceof Map<?, ?> auth) {
                    u.put("authType", auth.get("type"));
                }
                if (spec.get("authorization") instanceof Map<?, ?> authz) {
                    u.put("aclType", authz.get("type"));
                }

                // Ready status
                Map<String, Object> status = (Map<String, Object>) user.getAdditionalProperties()
                        .getOrDefault("status", Map.of());
                if (status.get("conditions") instanceof List<?> conditions) {
                    for (Object c : conditions) {
                        if (c instanceof Map<?, ?> cm && "Ready".equals(cm.get("type"))) {
                            u.put("ready", "True".equals(cm.get("status")));
                        }
                    }
                }
                userDetails.add(u);
            }
            userDetails.sort(Comparator.comparing(t -> (String) t.get("name")));
            users.put("items", userDetails);
        } catch (Exception e) {
            LOG.debug("Unable to read KafkaUsers", e);
            users.put("count", 0);
        }
        return users;
    }

    @SuppressWarnings("unchecked")
    private List<Map<String, Object>> describeConnect() {
        List<Map<String, Object>> connects = new ArrayList<>();
        try {
            var list = kubernetesClient.genericKubernetesResources(KAFKA_CONNECT_CRD)
                    .inNamespace(kafkaNamespace)
                    .list()
                    .getItems();
            for (GenericKubernetesResource res : list) {
                Map<String, Object> c = new LinkedHashMap<>();
                c.put("name", res.getMetadata().getName());
                Map<String, Object> spec = (Map<String, Object>) res.getAdditionalProperties()
                        .getOrDefault("spec", Map.of());
                if (spec.get("replicas") instanceof Number n) c.put("replicas", n.intValue());
                if (spec.get("bootstrapServers") != null) c.put("bootstrapServers", spec.get("bootstrapServers"));
                connects.add(c);
            }
        } catch (Exception e) {
            LOG.debug("Unable to read KafkaConnects", e);
        }
        return connects;
    }

    @SuppressWarnings("unchecked")
    private List<Map<String, Object>> describeMirrorMaker2() {
        List<Map<String, Object>> mm2s = new ArrayList<>();
        try {
            var list = kubernetesClient.genericKubernetesResources(KAFKA_MM2_CRD)
                    .inNamespace(kafkaNamespace)
                    .list()
                    .getItems();
            for (GenericKubernetesResource res : list) {
                Map<String, Object> m = new LinkedHashMap<>();
                m.put("name", res.getMetadata().getName());
                Map<String, Object> spec = (Map<String, Object>) res.getAdditionalProperties()
                        .getOrDefault("spec", Map.of());
                if (spec.get("replicas") instanceof Number n) m.put("replicas", n.intValue());
                mm2s.add(m);
            }
        } catch (Exception e) {
            LOG.debug("Unable to read KafkaMirrorMaker2s", e);
        }
        return mm2s;
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
