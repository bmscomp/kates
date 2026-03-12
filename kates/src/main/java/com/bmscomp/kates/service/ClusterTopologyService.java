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
import org.apache.kafka.clients.admin.ConsumerGroupListing;
import org.apache.kafka.clients.admin.DescribeClusterResult;
import org.apache.kafka.clients.admin.DescribeLogDirsResult;
import org.apache.kafka.clients.admin.LogDirDescription;
import org.apache.kafka.common.Node;
import org.apache.kafka.common.acl.AclBinding;
import org.apache.kafka.common.acl.AclBindingFilter;
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

    private static final CustomResourceDefinitionContext KAFKA_REBALANCE_CRD = new CustomResourceDefinitionContext.Builder()
            .withGroup("kafka.strimzi.io")
            .withVersion("v1")
            .withPlural("kafkarebalances")
            .withScope("Namespaced")
            .build();

    private static final CustomResourceDefinitionContext STRIMZI_POD_SET_CRD = new CustomResourceDefinitionContext.Builder()
            .withGroup("core.strimzi.io")
            .withVersion("v1beta2")
            .withPlural("strimzipodsets")
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

        // Read the Kafka CR once and share it across sections
        Map<String, Object> kafkaCrSpec = readKafkaCrSpec();

        topology.put("kubernetes", describeKubernetes());
        topology.put("strimzi", describeStrimzi());
        topology.put("cluster", describeKafkaCluster(kafkaCrSpec));
        topology.put("kafkaConfig", describeKafkaConfig(kafkaCrSpec));
        topology.put("nodePools", describeNodePools());
        topology.put("nodes", describeNodes());
        topology.put("entityOperator", describeEntityOperator(kafkaCrSpec));
        topology.put("cruiseControl", describeCruiseControl(kafkaCrSpec));
        topology.put("kafkaExporter", describeKafkaExporter(kafkaCrSpec));
        topology.put("certificates", describeCertificates(kafkaCrSpec));
        topology.put("metrics", describeMetrics(kafkaCrSpec));
        topology.put("topics", describeTopics());
        topology.put("users", describeUsers());
        topology.put("consumerGroups", describeConsumerGroups());
        topology.put("acls", describeAcls());
        topology.put("logDirs", describeLogDirs());
        topology.put("featureFlags", describeFeatureFlags());
        topology.put("rebalances", describeRebalances());
        topology.put("drainCleaner", describeDrainCleaner());
        topology.put("podSets", describeStrimziPodSets());
        topology.put("networkPolicies", describeNetworkPolicies());
        topology.put("pvcs", describePvcs());
        topology.put("services", describeServices());
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
    private Map<String, Object> readKafkaCrSpec() {
        try {
            GenericKubernetesResource kafka = kubernetesClient.genericKubernetesResources(KAFKA_CRD)
                    .inNamespace(kafkaNamespace)
                    .withName(kafkaCluster)
                    .get();
            if (kafka != null) {
                Map<String, Object> props = kafka.getAdditionalProperties();
                Map<String, Object> result = new LinkedHashMap<>();
                result.put("spec", props.getOrDefault("spec", Map.of()));
                result.put("status", props.getOrDefault("status", Map.of()));
                return result;
            }
        } catch (Exception e) {
            LOG.warn("Failed to read Kafka CR", e);
        }
        return Map.of();
    }

    @SuppressWarnings("unchecked")
    private Map<String, Object> describeKafkaCluster(Map<String, Object> kafkaCr) {
        Map<String, Object> cluster = new LinkedHashMap<>();
        cluster.put("name", kafkaCluster);
        cluster.put("namespace", kafkaNamespace);
        cluster.put("kraftMode", true);

        String kafkaVersion = "unknown";
        Map<String, Object> spec = (Map<String, Object>) kafkaCr.getOrDefault("spec", Map.of());
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
                    if (lm.get("configuration") instanceof Map<?, ?> conf) {
                        li.put("configuration", conf);
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

        // Rack awareness
        if (kafkaSpec.get("rack") instanceof Map<?, ?> rack) {
            cluster.put("rackAwareness", rack);
        }

        // PDB
        if (kafkaSpec.get("template") instanceof Map<?, ?> template) {
            if (template.get("podDisruptionBudget") instanceof Map<?, ?> pdb) {
                cluster.put("podDisruptionBudget", pdb);
            }
        }

        // Status from CR
        Map<String, Object> status = (Map<String, Object>) kafkaCr.getOrDefault("status", Map.of());
        if (status.get("conditions") instanceof List<?> conditions) {
            for (Object c : conditions) {
                if (c instanceof Map<?, ?> cm && "Ready".equals(cm.get("type"))) {
                    cluster.put("ready", "True".equals(cm.get("status")));
                }
            }
        }
        if (status.get("kafkaVersion") != null) {
            kafkaVersion = status.get("kafkaVersion").toString();
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
    private Map<String, Object> describeKafkaConfig(Map<String, Object> kafkaCr) {
        Map<String, Object> spec = (Map<String, Object>) kafkaCr.getOrDefault("spec", Map.of());
        Map<String, Object> kafkaSpec = (Map<String, Object>) spec.getOrDefault("kafka", Map.of());
        if (kafkaSpec.get("config") instanceof Map<?, ?> config) {
            return new LinkedHashMap<>((Map<String, Object>) config);
        }
        return Map.of();
    }

    @SuppressWarnings("unchecked")
    private Map<String, Object> describeEntityOperator(Map<String, Object> kafkaCr) {
        Map<String, Object> result = new LinkedHashMap<>();
        Map<String, Object> spec = (Map<String, Object>) kafkaCr.getOrDefault("spec", Map.of());
        if (spec.get("entityOperator") instanceof Map<?, ?> eo) {
            if (eo.get("topicOperator") instanceof Map<?, ?> to) {
                Map<String, Object> topicOp = new LinkedHashMap<>();
                if (to.get("resources") != null) topicOp.put("resources", to.get("resources"));
                if (to.get("jvmOptions") != null) topicOp.put("jvmOptions", to.get("jvmOptions"));
                if (to.get("reconciliationIntervalMs") != null)
                    topicOp.put("reconciliationIntervalMs", to.get("reconciliationIntervalMs"));
                result.put("topicOperator", topicOp);
            }
            if (eo.get("userOperator") instanceof Map<?, ?> uo) {
                Map<String, Object> userOp = new LinkedHashMap<>();
                if (uo.get("resources") != null) userOp.put("resources", uo.get("resources"));
                if (uo.get("jvmOptions") != null) userOp.put("jvmOptions", uo.get("jvmOptions"));
                result.put("userOperator", userOp);
            }
        }
        return result;
    }

    @SuppressWarnings("unchecked")
    private Map<String, Object> describeCruiseControl(Map<String, Object> kafkaCr) {
        Map<String, Object> result = new LinkedHashMap<>();
        Map<String, Object> spec = (Map<String, Object>) kafkaCr.getOrDefault("spec", Map.of());
        if (spec.get("cruiseControl") instanceof Map<?, ?> cc) {
            if (cc.get("brokerCapacity") != null) result.put("brokerCapacity", cc.get("brokerCapacity"));
            if (cc.get("autoRebalance") != null) result.put("autoRebalance", cc.get("autoRebalance"));
            if (cc.get("resources") != null) result.put("resources", cc.get("resources"));
            if (cc.get("config") != null) result.put("config", cc.get("config"));
            if (cc.get("metricsConfig") != null) result.put("metricsConfig", cc.get("metricsConfig"));
        }
        return result;
    }

    @SuppressWarnings("unchecked")
    private Map<String, Object> describeKafkaExporter(Map<String, Object> kafkaCr) {
        Map<String, Object> result = new LinkedHashMap<>();
        Map<String, Object> spec = (Map<String, Object>) kafkaCr.getOrDefault("spec", Map.of());
        if (spec.get("kafkaExporter") instanceof Map<?, ?> ke) {
            if (ke.get("topicRegex") != null) result.put("topicRegex", ke.get("topicRegex"));
            if (ke.get("groupRegex") != null) result.put("groupRegex", ke.get("groupRegex"));
            if (ke.get("resources") != null) result.put("resources", ke.get("resources"));
        }
        return result;
    }

    @SuppressWarnings("unchecked")
    private Map<String, Object> describeCertificates(Map<String, Object> kafkaCr) {
        Map<String, Object> result = new LinkedHashMap<>();
        Map<String, Object> spec = (Map<String, Object>) kafkaCr.getOrDefault("spec", Map.of());
        if (spec.get("clusterCa") instanceof Map<?, ?> clusterCa) {
            result.put("clusterCa", new LinkedHashMap<>(clusterCa));
        }
        if (spec.get("clientsCa") instanceof Map<?, ?> clientsCa) {
            result.put("clientsCa", new LinkedHashMap<>(clientsCa));
        }
        return result;
    }

    @SuppressWarnings("unchecked")
    private Map<String, Object> describeMetrics(Map<String, Object> kafkaCr) {
        Map<String, Object> result = new LinkedHashMap<>();
        Map<String, Object> spec = (Map<String, Object>) kafkaCr.getOrDefault("spec", Map.of());
        Map<String, Object> kafkaSpec = (Map<String, Object>) spec.getOrDefault("kafka", Map.of());
        if (kafkaSpec.get("metricsConfig") instanceof Map<?, ?> mc) {
            result.put("kafka", mc);
        }
        // PodMonitors
        try {
            var monitors = kubernetesClient.genericKubernetesResources(
                    new CustomResourceDefinitionContext.Builder()
                            .withGroup("monitoring.coreos.com")
                            .withVersion("v1")
                            .withPlural("podmonitors")
                            .withScope("Namespaced")
                            .build()
            ).inNamespace(kafkaNamespace).list().getItems();
            List<String> monitorNames = new ArrayList<>();
            for (var m : monitors) {
                monitorNames.add(m.getMetadata().getName());
            }
            if (!monitorNames.isEmpty()) {
                result.put("podMonitors", monitorNames);
            }
        } catch (Exception e) {
            LOG.debug("Unable to read PodMonitors", e);
        }
        return result;
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
                String storageClass = "";
                if (storage.get("volumes") instanceof List<?> volumes && !volumes.isEmpty()) {
                    Map<String, Object> vol = (Map<String, Object>) volumes.get(0);
                    storageSize = (String) vol.getOrDefault("size", "");
                    if (vol.get("class") != null) storageClass = vol.get("class").toString();
                }

                Map<String, Object> resources = (Map<String, Object>) spec.getOrDefault("resources", Map.of());

                Map<String, Object> poolInfo = new LinkedHashMap<>();
                poolInfo.put("name", poolName);
                poolInfo.put("role", role);
                poolInfo.put("replicas", replicas);
                poolInfo.put("storageType", storageType);
                poolInfo.put("storageSize", storageSize);
                if (!storageClass.isEmpty()) poolInfo.put("storageClass", storageClass);
                if (!resources.isEmpty()) poolInfo.put("resources", resources);

                // JVM options
                if (spec.get("jvmOptions") instanceof Map<?, ?> jvm) {
                    poolInfo.put("jvmOptions", jvm);
                }

                // Scheduling (affinity, tolerations, topology spread)
                if (spec.get("template") instanceof Map<?, ?> template) {
                    if (template.get("pod") instanceof Map<?, ?> pod) {
                        Map<String, Object> scheduling = new LinkedHashMap<>();
                        if (pod.get("affinity") != null) scheduling.put("affinity", true);
                        if (pod.get("tolerations") instanceof List<?> tols && !tols.isEmpty()) {
                            scheduling.put("tolerations", tols.size());
                        }
                        if (pod.get("topologySpreadConstraints") instanceof List<?> tsc && !tsc.isEmpty()) {
                            scheduling.put("topologySpreadConstraints", tsc.size());
                        }
                        if (pod.get("metadata") instanceof Map<?, ?> meta
                                && meta.get("labels") instanceof Map<?, ?> labels
                                && labels.get("zone") != null) {
                            scheduling.put("zone", labels.get("zone"));
                        }
                        if (!scheduling.isEmpty()) poolInfo.put("scheduling", scheduling);
                    }
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

    private List<Map<String, Object>> describeLogDirs() {
        List<Map<String, Object>> result = new ArrayList<>();
        try {
            AdminClient client = adminService.getClient();
            Collection<Node> nodes = client.describeCluster().nodes().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            List<Integer> brokerIds = nodes.stream().map(Node::id).toList();
            DescribeLogDirsResult logDirs = client.describeLogDirs(brokerIds);
            Map<Integer, Map<String, LogDirDescription>> allLogDirs = logDirs.allDescriptions()
                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            for (var entry : allLogDirs.entrySet()) {
                int brokerId = entry.getKey();
                for (var dirEntry : entry.getValue().entrySet()) {
                    Map<String, Object> dir = new LinkedHashMap<>();
                    dir.put("brokerId", brokerId);
                    dir.put("path", dirEntry.getKey());
                    LogDirDescription desc = dirEntry.getValue();
                    long totalBytes = desc.replicaInfos().values().stream()
                            .mapToLong(r -> r.size())
                            .sum();
                    dir.put("sizeMb", totalBytes / (1024 * 1024));
                    dir.put("partitions", desc.replicaInfos().size());
                    if (desc.error() != null) dir.put("error", desc.error().getMessage());
                    result.add(dir);
                }
            }
        } catch (Exception e) {
            LOG.debug("Unable to describe log dirs", e);
        }
        result.sort(Comparator.comparingInt(d -> (int) d.get("brokerId")));
        return result;
    }

    private Map<String, Object> describeConsumerGroups() {
        Map<String, Object> result = new LinkedHashMap<>();
        try {
            AdminClient client = adminService.getClient();
            Collection<ConsumerGroupListing> groups = client.listConsumerGroups()
                    .all().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            List<Map<String, Object>> items = new ArrayList<>();
            if (!groups.isEmpty()) {
                var descriptions = client.describeConsumerGroups(
                        groups.stream().map(ConsumerGroupListing::groupId).toList()
                ).all().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
                for (var entry : descriptions.entrySet()) {
                    Map<String, Object> g = new LinkedHashMap<>();
                    g.put("groupId", entry.getKey());
                    g.put("state", entry.getValue().state().toString());
                    g.put("type", entry.getValue().type().toString());
                    g.put("members", entry.getValue().members().size());
                    if (entry.getValue().coordinator() != null) {
                        g.put("coordinator", entry.getValue().coordinator().id());
                    }
                    items.add(g);
                }
            }
            result.put("count", items.size());
            result.put("items", items);
        } catch (Exception e) {
            LOG.debug("Unable to describe consumer groups", e);
            result.put("count", 0);
            result.put("items", List.of());
        }
        return result;
    }

    private Map<String, Object> describeAcls() {
        Map<String, Object> result = new LinkedHashMap<>();
        try {
            AdminClient client = adminService.getClient();
            Collection<AclBinding> acls = client.describeAcls(AclBindingFilter.ANY)
                    .values().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            List<Map<String, Object>> items = new ArrayList<>();
            for (AclBinding acl : acls) {
                Map<String, Object> a = new LinkedHashMap<>();
                a.put("principal", acl.entry().principal());
                a.put("resourceType", acl.pattern().resourceType().toString());
                a.put("resourceName", acl.pattern().name());
                a.put("operation", acl.entry().operation().toString());
                a.put("permission", acl.entry().permissionType().toString());
                a.put("host", acl.entry().host());
                items.add(a);
            }
            items.sort(Comparator.comparing((Map<String, Object> a) -> (String) a.get("principal"))
                    .thenComparing(a -> (String) a.get("resourceType"))
                    .thenComparing(a -> (String) a.get("resourceName")));
            result.put("count", items.size());
            result.put("items", items);
        } catch (Exception e) {
            LOG.debug("Unable to describe ACLs", e);
            result.put("count", 0);
            result.put("items", List.of());
        }
        return result;
    }

    private Map<String, Object> describeFeatureFlags() {
        Map<String, Object> result = new LinkedHashMap<>();
        try {
            AdminClient client = adminService.getClient();
            var features = client.describeFeatures().featureMetadata()
                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            var finalized = features.finalizedFeatures();
            List<Map<String, Object>> items = new ArrayList<>();
            for (var entry : finalized.entrySet()) {
                Map<String, Object> f = new LinkedHashMap<>();
                f.put("name", entry.getKey());
                f.put("minVersion", entry.getValue().minVersionLevel());
                f.put("maxVersion", entry.getValue().maxVersionLevel());
                items.add(f);
            }
            items.sort(Comparator.comparing(f -> (String) f.get("name")));
            result.put("count", items.size());
            result.put("items", items);
        } catch (Exception e) {
            LOG.debug("Unable to describe feature flags", e);
            result.put("count", 0);
            result.put("items", List.of());
        }
        return result;
    }

    @SuppressWarnings("unchecked")
    private List<Map<String, Object>> describeRebalances() {
        List<Map<String, Object>> result = new ArrayList<>();
        try {
            var list = kubernetesClient.genericKubernetesResources(KAFKA_REBALANCE_CRD)
                    .inNamespace(kafkaNamespace)
                    .list()
                    .getItems();
            for (GenericKubernetesResource res : list) {
                Map<String, Object> r = new LinkedHashMap<>();
                r.put("name", res.getMetadata().getName());
                Map<String, Object> spec = (Map<String, Object>) res.getAdditionalProperties()
                        .getOrDefault("spec", Map.of());
                if (spec.get("mode") != null) r.put("mode", spec.get("mode"));
                if (spec.get("goals") instanceof List<?> goals) r.put("goalCount", goals.size());
                if (spec.get("rebalanceDisk") instanceof Boolean rd) r.put("rebalanceDisk", rd);
                Map<String, Object> status = (Map<String, Object>) res.getAdditionalProperties()
                        .getOrDefault("status", Map.of());
                if (status.get("conditions") instanceof List<?> conditions && !conditions.isEmpty()) {
                    Map<String, Object> last = (Map<String, Object>) conditions.get(conditions.size() - 1);
                    r.put("status", last.getOrDefault("type", "Unknown"));
                    r.put("lastTransition", last.get("lastTransitionTime"));
                } else {
                    r.put("status", "Unknown");
                }
                result.add(r);
            }
        } catch (Exception e) {
            LOG.debug("Unable to read KafkaRebalances", e);
        }
        return result;
    }

    private Map<String, Object> describeDrainCleaner() {
        Map<String, Object> result = new LinkedHashMap<>();
        try {
            var deploy = kubernetesClient.apps().deployments()
                    .inNamespace(kafkaNamespace)
                    .withName("strimzi-drain-cleaner")
                    .get();
            if (deploy != null) {
                result.put("ready", deploy.getStatus() != null
                        && deploy.getStatus().getReadyReplicas() != null
                        && deploy.getStatus().getReadyReplicas() > 0);
                result.put("replicas", deploy.getSpec().getReplicas());
                if (deploy.getStatus() != null && deploy.getStatus().getReadyReplicas() != null) {
                    result.put("readyReplicas", deploy.getStatus().getReadyReplicas());
                }
                var containers = deploy.getSpec().getTemplate().getSpec().getContainers();
                if (!containers.isEmpty()) {
                    result.put("image", containers.get(0).getImage());
                    var envVars = containers.get(0).getEnv();
                    if (envVars != null) {
                        Map<String, String> config = new LinkedHashMap<>();
                        for (var env : envVars) {
                            config.put(env.getName(), env.getValue());
                        }
                        result.put("config", config);
                    }
                }
            }
        } catch (Exception e) {
            LOG.debug("Unable to describe Drain Cleaner", e);
        }
        return result;
    }

    @SuppressWarnings("unchecked")
    private List<Map<String, Object>> describeStrimziPodSets() {
        List<Map<String, Object>> result = new ArrayList<>();
        try {
            var list = kubernetesClient.genericKubernetesResources(STRIMZI_POD_SET_CRD)
                    .inNamespace(kafkaNamespace)
                    .list()
                    .getItems();
            for (GenericKubernetesResource res : list) {
                Map<String, Object> ps = new LinkedHashMap<>();
                ps.put("name", res.getMetadata().getName());
                Map<String, Object> status = (Map<String, Object>) res.getAdditionalProperties()
                        .getOrDefault("status", Map.of());
                if (status.get("pods") instanceof Number n) ps.put("pods", n.intValue());
                if (status.get("readyPods") instanceof Number n) ps.put("readyPods", n.intValue());
                if (status.get("currentPods") instanceof Number n) ps.put("currentPods", n.intValue());
                Map<String, Object> spec = (Map<String, Object>) res.getAdditionalProperties()
                        .getOrDefault("spec", Map.of());
                if (spec.get("pods") instanceof List<?> pods) ps.put("desiredPods", pods.size());
                result.add(ps);
            }
        } catch (Exception e) {
            LOG.debug("Unable to read StrimziPodSets", e);
        }
        return result;
    }

    private List<Map<String, Object>> describeNetworkPolicies() {
        List<Map<String, Object>> result = new ArrayList<>();
        try {
            var policies = kubernetesClient.network().networkPolicies()
                    .inNamespace(kafkaNamespace)
                    .list()
                    .getItems();
            for (var np : policies) {
                Map<String, Object> p = new LinkedHashMap<>();
                p.put("name", np.getMetadata().getName());
                var podSel = np.getSpec().getPodSelector();
                if (podSel != null && podSel.getMatchLabels() != null && !podSel.getMatchLabels().isEmpty()) {
                    p.put("targetPods", podSel.getMatchLabels());
                } else {
                    p.put("targetPods", "all");
                }
                p.put("policyTypes", np.getSpec().getPolicyTypes());
                if (np.getSpec().getIngress() != null) {
                    p.put("ingressRules", np.getSpec().getIngress().size());
                }
                if (np.getSpec().getEgress() != null) {
                    p.put("egressRules", np.getSpec().getEgress().size());
                }
                result.add(p);
            }
        } catch (Exception e) {
            LOG.debug("Unable to list NetworkPolicies", e);
        }
        return result;
    }

    private List<Map<String, Object>> describePvcs() {
        List<Map<String, Object>> result = new ArrayList<>();
        try {
            var pvcs = kubernetesClient.persistentVolumeClaims()
                    .inNamespace(kafkaNamespace)
                    .list()
                    .getItems();
            for (var pvc : pvcs) {
                Map<String, Object> p = new LinkedHashMap<>();
                p.put("name", pvc.getMetadata().getName());
                p.put("status", pvc.getStatus() != null ? pvc.getStatus().getPhase() : "Unknown");
                if (pvc.getSpec().getStorageClassName() != null) {
                    p.put("storageClass", pvc.getSpec().getStorageClassName());
                }
                var capacity = pvc.getStatus() != null ? pvc.getStatus().getCapacity() : null;
                if (capacity != null && capacity.get("storage") != null) {
                    p.put("capacity", capacity.get("storage").toString());
                } else if (pvc.getSpec().getResources() != null
                        && pvc.getSpec().getResources().getRequests() != null
                        && pvc.getSpec().getResources().getRequests().get("storage") != null) {
                    p.put("capacity", pvc.getSpec().getResources().getRequests().get("storage").toString());
                }
                if (pvc.getSpec().getAccessModes() != null) {
                    p.put("accessModes", pvc.getSpec().getAccessModes());
                }
                var labels = pvc.getMetadata().getLabels();
                if (labels != null && labels.get("strimzi.io/pool-name") != null) {
                    p.put("nodePool", labels.get("strimzi.io/pool-name"));
                }
                result.add(p);
            }
        } catch (Exception e) {
            LOG.debug("Unable to list PVCs", e);
        }
        return result;
    }

    private List<Map<String, Object>> describeServices() {
        List<Map<String, Object>> result = new ArrayList<>();
        try {
            var svcs = kubernetesClient.services()
                    .inNamespace(kafkaNamespace)
                    .list()
                    .getItems();
            for (var svc : svcs) {
                Map<String, Object> s = new LinkedHashMap<>();
                s.put("name", svc.getMetadata().getName());
                s.put("type", svc.getSpec().getType());
                s.put("clusterIP", svc.getSpec().getClusterIP());
                List<Map<String, Object>> ports = new ArrayList<>();
                if (svc.getSpec().getPorts() != null) {
                    for (var port : svc.getSpec().getPorts()) {
                        Map<String, Object> portInfo = new LinkedHashMap<>();
                        if (port.getName() != null) portInfo.put("name", port.getName());
                        portInfo.put("port", port.getPort());
                        portInfo.put("protocol", port.getProtocol());
                        if (port.getNodePort() != null && port.getNodePort() > 0) {
                            portInfo.put("nodePort", port.getNodePort());
                        }
                        ports.add(portInfo);
                    }
                }
                s.put("ports", ports);
                if (svc.getSpec().getSelector() != null) {
                    s.put("selector", svc.getSpec().getSelector());
                }
                result.add(s);
            }
        } catch (Exception e) {
            LOG.debug("Unable to list Services", e);
        }
        return result;
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
