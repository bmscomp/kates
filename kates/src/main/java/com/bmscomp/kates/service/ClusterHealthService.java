package com.bmscomp.kates.service;

import java.util.ArrayList;
import java.util.Collection;
import java.util.Collections;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.Config;
import org.apache.kafka.clients.admin.ConfigEntry;
import org.apache.kafka.clients.admin.DescribeClusterResult;
import org.apache.kafka.clients.admin.GroupListing;
import org.apache.kafka.clients.admin.ListGroupsOptions;
import org.apache.kafka.clients.admin.TopicDescription;
import org.apache.kafka.common.Node;
import org.apache.kafka.common.TopicPartitionInfo;
import org.apache.kafka.common.config.ConfigResource;
import org.eclipse.microprofile.faulttolerance.Retry;
import org.eclipse.microprofile.faulttolerance.Timeout;
import org.jboss.logging.Logger;

import com.bmscomp.kates.report.ClusterSnapshot;

@ApplicationScoped
public class ClusterHealthService {

    private static final Logger LOG = Logger.getLogger(ClusterHealthService.class);
    private static final int TIMEOUT_SECONDS = 30;
    private static final long CACHE_TTL_MS = 30_000;

    private final KafkaAdminService adminService;

    private volatile Map<String, Object> cachedClusterInfo;
    private volatile long clusterCacheExpiry;

    private volatile Map<String, Object> cachedHealthCheck;
    private volatile long healthCacheExpiry;

    @Inject
    public ClusterHealthService(KafkaAdminService adminService) {
        this.adminService = adminService;
    }

    public void evictCache() {
        clusterCacheExpiry = 0;
        cachedClusterInfo = null;
    }

    public void evictHealthCache() {
        healthCacheExpiry = 0;
        cachedHealthCheck = null;
    }

    @Retry(maxRetries = 2, delay = 1000)
    @Timeout(35_000)
    public Map<String, Object> describeCluster() {
        if (cachedClusterInfo != null && System.currentTimeMillis() < clusterCacheExpiry) {
            return cachedClusterInfo;
        }
        try {
            AdminClient client = adminService.getClient();
            DescribeClusterResult cluster = client.describeCluster();
            Map<String, Object> info = new HashMap<>();
            info.put("clusterId", cluster.clusterId().get(TIMEOUT_SECONDS, TimeUnit.SECONDS));
            info.put("controller", nodeToMap(cluster.controller().get(TIMEOUT_SECONDS, TimeUnit.SECONDS)));

            Collection<Node> nodes = cluster.nodes().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            info.put("brokerCount", nodes.size());
            info.put("brokers", nodes.stream().map(this::nodeToMap).toList());

            cachedClusterInfo = info;
            clusterCacheExpiry = System.currentTimeMillis() + CACHE_TTL_MS;
            return info;
        } catch (Exception e) {
            throw new RuntimeException("Failed to describe cluster", e);
        }
    }

    @Timeout(10_000)
    public boolean isReachable() {
        AdminClient client = adminService.getClient();
        try {
            client.describeCluster().clusterId().get(5, TimeUnit.SECONDS);
            return true;
        } catch (Exception e) {
            return false;
        }
    }

    public int brokerCount() {
        try {
            Object count = describeCluster().get("brokerCount");
            return count instanceof Integer ? (Integer) count : 0;
        } catch (Exception e) {
            return 0;
        }
    }

    private Map<String, Object> nodeToMap(Node node) {
        Map<String, Object> map = new HashMap<>();
        map.put("id", node.id());
        map.put("host", node.host());
        map.put("port", node.port());
        if (node.rack() != null) {
            map.put("rack", node.rack());
        }
        return map;
    }

    @Timeout(35_000)
    public List<Map<String, Object>> describeBrokerConfigs(int brokerId) {
        AdminClient client = adminService.getClient();
        try {
            ConfigResource resource = new ConfigResource(ConfigResource.Type.BROKER, String.valueOf(brokerId));
            Config config = client.describeConfigs(Collections.singleton(resource))
                    .all()
                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS)
                    .get(resource);

            List<Map<String, Object>> result = new ArrayList<>();
            for (ConfigEntry entry : config.entries()) {
                if (entry.source() == ConfigEntry.ConfigSource.DEFAULT_CONFIG && !entry.isReadOnly()) {
                    continue;
                }
                if (entry.source() == ConfigEntry.ConfigSource.DEFAULT_CONFIG) {
                    continue;
                }
                Map<String, Object> item = new LinkedHashMap<>();
                item.put("name", entry.name());
                item.put("value", entry.value());
                item.put("source", entry.source().toString());
                item.put("readOnly", entry.isReadOnly());
                result.add(item);
            }
            return result;
        } catch (Exception e) {
            throw new RuntimeException("Failed to describe broker configs for broker " + brokerId, e);
        }
    }

    @Timeout(35_000)
    public ClusterSnapshot captureSnapshot(String topicName) {
        AdminClient client = adminService.getClient();
        try {
            DescribeClusterResult cluster = client.describeCluster();
            String clusterId = cluster.clusterId().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            Node controller = cluster.controller().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            Collection<Node> nodes = cluster.nodes().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);

            List<ClusterSnapshot.BrokerInfo> brokers = nodes.stream()
                    .map(n -> new ClusterSnapshot.BrokerInfo(n.id(), n.host(), n.port(), n.rack()))
                    .toList();

            List<ClusterSnapshot.PartitionAssignment> leaders = new ArrayList<>();
            if (topicName != null && !topicName.isBlank()) {
                TopicDescription desc = client.describeTopics(Collections.singleton(topicName))
                        .allTopicNames()
                        .get(TIMEOUT_SECONDS, TimeUnit.SECONDS)
                        .get(topicName);

                if (desc != null) {
                    for (TopicPartitionInfo pi : desc.partitions()) {
                        leaders.add(new ClusterSnapshot.PartitionAssignment(
                                topicName,
                                pi.partition(),
                                pi.leader() != null ? pi.leader().id() : -1,
                                pi.replicas().stream().map(Node::id).toList(),
                                pi.isr().stream().map(Node::id).toList()));
                    }
                }
            }

            return new ClusterSnapshot(clusterId, nodes.size(), controller.id(), brokers, leaders);
        } catch (Exception e) {
            LOG.warn("Failed to capture cluster snapshot", e);
            return null;
        }
    }

    @Retry(maxRetries = 2, delay = 1000)
    @Timeout(60_000)
    public Map<String, Object> clusterHealthCheck() {
        if (cachedHealthCheck != null && System.currentTimeMillis() < healthCacheExpiry) {
            return cachedHealthCheck;
        }

        AdminClient client = adminService.getClient();
        try {
            Map<String, Object> report = new LinkedHashMap<>();

            DescribeClusterResult cluster = client.describeCluster();

            var clusterIdFuture = CompletableFuture.supplyAsync(() -> {
                try {
                    return cluster.clusterId().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
                } catch (Exception e) {
                    throw new RuntimeException(e);
                }
            });
            var controllerFuture = CompletableFuture.supplyAsync(() -> {
                try {
                    return cluster.controller().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
                } catch (Exception e) {
                    throw new RuntimeException(e);
                }
            });
            var nodesFuture = CompletableFuture.supplyAsync(() -> {
                try {
                    return cluster.nodes().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
                } catch (Exception e) {
                    throw new RuntimeException(e);
                }
            });

            var topicsFuture = CompletableFuture.supplyAsync(() -> {
                try {
                    return client.listTopics().names().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
                } catch (Exception e) {
                    throw new RuntimeException(e);
                }
            });

            var groupsFuture = CompletableFuture.supplyAsync(() -> {
                try {
                    return client.listGroups(ListGroupsOptions.forConsumerGroups())
                            .all()
                            .get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
                } catch (Exception e) {
                    throw new RuntimeException(e);
                }
            });

            CompletableFuture.allOf(clusterIdFuture, controllerFuture, nodesFuture, topicsFuture, groupsFuture).join();

            String clusterId = clusterIdFuture.join();
            Node controller = controllerFuture.join();
            Collection<Node> nodes = nodesFuture.join();
            Set<String> topics = topicsFuture.join();
            Collection<GroupListing> groups = groupsFuture.join();

            report.put("clusterId", clusterId);
            report.put("brokers", nodes.size());
            report.put("controllerId", controller.id());

            Map<String, TopicDescription> topicDescs = topics.isEmpty()
                    ? Map.of()
                    : client.describeTopics(topics).allTopicNames().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);

            int totalPartitions = 0;
            int underReplicated = 0;
            int offlinePartitions = 0;
            List<Map<String, Object>> problems = new ArrayList<>();

            for (var entry : topicDescs.entrySet()) {
                for (TopicPartitionInfo pi : entry.getValue().partitions()) {
                    totalPartitions++;
                    if (pi.leader() == null || pi.leader().id() < 0) {
                        offlinePartitions++;
                        Map<String, Object> problem = new LinkedHashMap<>();
                        problem.put("topic", entry.getKey());
                        problem.put("partition", pi.partition());
                        problem.put("issue", "OFFLINE");
                        problems.add(problem);
                    } else if (pi.isr().size() < pi.replicas().size()) {
                        underReplicated++;
                        Map<String, Object> problem = new LinkedHashMap<>();
                        problem.put("topic", entry.getKey());
                        problem.put("partition", pi.partition());
                        problem.put("issue", "UNDER_REPLICATED");
                        problem.put("isr", pi.isr().size());
                        problem.put("replicas", pi.replicas().size());
                        problems.add(problem);
                    }
                }
            }

            report.put("topics", topics.size());
            report.put("partitions", totalPartitions);

            Map<String, Object> partitionHealth = new LinkedHashMap<>();
            partitionHealth.put("underReplicated", underReplicated);
            partitionHealth.put("offline", offlinePartitions);
            partitionHealth.put("problems", problems);
            report.put("partitionHealth", partitionHealth);

            report.put("consumerGroups", groups.size());

            String status = "HEALTHY";
            if (offlinePartitions > 0) {
                status = "CRITICAL";
            } else if (underReplicated > 0) {
                status = "WARNING";
            }
            report.put("status", status);

            cachedHealthCheck = report;
            healthCacheExpiry = System.currentTimeMillis() + CACHE_TTL_MS;

            return report;
        } catch (Exception e) {
            throw new RuntimeException("Failed to perform cluster health check", e);
        }
    }
}

