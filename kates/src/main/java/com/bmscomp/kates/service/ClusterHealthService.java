package com.bmscomp.kates.service;

import java.util.ArrayList;
import java.util.Collection;
import java.util.Collections;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.TimeUnit;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.smallrye.mutiny.Uni;
import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.Config;
import org.apache.kafka.clients.admin.ConfigEntry;
import org.apache.kafka.clients.admin.ConsumerGroupListing;
import org.apache.kafka.clients.admin.DescribeClusterResult;
import org.apache.kafka.clients.admin.ListConsumerGroupsOptions;
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

    /**
     * Deadline for the advisory lookups that would rather answer "unknown" than
     * make the caller wait. Matches {@link #isReachable()}'s bound on purpose:
     * both are asking whether a cluster is there at all.
     */
    private static final int BEST_EFFORT_TIMEOUT_SECONDS = 5;

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

    /**
     * Best-effort broker count, bounded so an absent cluster costs seconds
     * rather than most of a minute.
     *
     * <p>This used to delegate to {@link #describeCluster()}, which opens with a
     * 30-second get on the cluster id. Its {@code @Retry} and {@code @Timeout}
     * do not bound that from here — a self-invocation inside the bean bypasses
     * the interceptors — so with no broker reachable every call blocked for the
     * full 30 seconds before returning the 0 the catch block was always going to
     * return, and failures are never cached, so the next caller paid it again.
     *
     * <p>Both callers advise on this number rather than depend on it — the
     * advisor weighs it into a recommendation, the cost endpoint into an
     * estimate — and both already read 0 as "no cluster information". Neither is
     * worth a 35-second request. The IT suite is where it surfaced: {@code
     * /api/tests/{id}/advisor} outlived the HTTP client's patience and the run
     * failed with "Read timed out", intermittently, depending on which test
     * raced the timeout first.
     *
     * <p>A warm cache still answers instantly and exactly. Only the cold path is
     * bounded, and it deliberately does not retry: if one describe against a
     * 5-second deadline did not answer, two more will not either. It also does
     * not populate the cache — a count taken under a short deadline is not the
     * full cluster picture {@link #describeCluster()} promises its callers.
     */
    public int brokerCount() {
        Map<String, Object> cached = cachedClusterInfo;
        if (cached != null && System.currentTimeMillis() < clusterCacheExpiry) {
            return cached.get("brokerCount") instanceof Integer count ? count : 0;
        }
        try {
            Collection<Node> nodes = adminService
                    .getClient()
                    .describeCluster()
                    .nodes()
                    .get(BEST_EFFORT_TIMEOUT_SECONDS, TimeUnit.SECONDS);
            return nodes == null ? 0 : nodes.size();
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            return 0;
        } catch (Exception e) {
            LOG.debugf("Broker count unavailable (%s); advising without it", e.toString());
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
    public Uni<Map<String, Object>> clusterHealthCheck() {
        if (cachedHealthCheck != null && System.currentTimeMillis() < healthCacheExpiry) {
            return Uni.createFrom().item(cachedHealthCheck);
        }

        AdminClient client = adminService.getClient();
        try {
            DescribeClusterResult cluster = client.describeCluster();

            Uni<String> clusterIdUni =
                    Uni.createFrom().completionStage(cluster.clusterId().toCompletionStage());
            Uni<Node> controllerUni =
                    Uni.createFrom().completionStage(cluster.controller().toCompletionStage());
            Uni<Collection<Node>> nodesUni =
                    Uni.createFrom().completionStage(cluster.nodes().toCompletionStage());
            Uni<Set<String>> topicsUni =
                    Uni.createFrom().completionStage(client.listTopics().names().toCompletionStage());
            Uni<Collection<ConsumerGroupListing>> groupsUni = Uni.createFrom()
                    .completionStage(client.listConsumerGroups(new ListConsumerGroupsOptions())
                            .all()
                            .toCompletionStage());

            // Note: describeMetadataQuorum might not be supported on all versions, so we fall back gracefully.
            Uni<org.apache.kafka.clients.admin.QuorumInfo> quorumUni = Uni.createFrom()
                    .completionStage(
                            client.describeMetadataQuorum().quorumInfo().toCompletionStage())
                    .onFailure()
                    .recoverWithNull();

            return Uni.combine()
                    .all()
                    .unis(clusterIdUni, controllerUni, nodesUni, topicsUni, groupsUni, quorumUni)
                    .asTuple()
                    .chain(tuple -> processHealthTuple(client, tuple));
        } catch (Exception e) {
            return Uni.createFrom().failure(new RuntimeException("Failed to perform cluster health check", e));
        }
    }

    private Uni<Map<String, Object>> processHealthTuple(
            AdminClient client,
            io.smallrye.mutiny.tuples.Tuple6<
                            String,
                            Node,
                            Collection<Node>,
                            Set<String>,
                            Collection<ConsumerGroupListing>,
                            org.apache.kafka.clients.admin.QuorumInfo>
                    tuple) {
        String clusterId = tuple.getItem1();
        Node controller = tuple.getItem2();
        Collection<Node> nodes = tuple.getItem3();
        Set<String> topics = tuple.getItem4();
        Collection<ConsumerGroupListing> groups = tuple.getItem5();
        org.apache.kafka.clients.admin.QuorumInfo quorum = tuple.getItem6();

        Map<String, Object> report = new LinkedHashMap<>();
        report.put("clusterId", clusterId);
        report.put("brokers", nodes.size());
        report.put("controllerId", controller.id());

        Uni<Map<String, TopicDescription>> topicDescsUni = topics.isEmpty()
                ? Uni.createFrom().item(Map.of())
                : Uni.createFrom()
                        .completionStage(
                                client.describeTopics(topics).allTopicNames().toCompletionStage());

        return topicDescsUni.map(topicDescs -> {
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

            if (quorum != null) {
                Map<String, Object> quorumHealth = new LinkedHashMap<>();
                quorumHealth.put("leaderId", quorum.leaderId());
                quorumHealth.put("voters", quorum.voters().size());
                quorumHealth.put("observers", quorum.observers().size());

                boolean hasLeader = quorum.leaderId() >= 0;
                quorumHealth.put("hasLeader", hasLeader);

                if (!hasLeader) {
                    status = "CRITICAL";
                    quorumHealth.put("issue", "NO_LEADER");
                }
                report.put("kraftQuorum", quorumHealth);
            }

            report.put("status", status);

            cachedHealthCheck = report;
            healthCacheExpiry = System.currentTimeMillis() + CACHE_TTL_MS;

            return report;
        });
    }
}
