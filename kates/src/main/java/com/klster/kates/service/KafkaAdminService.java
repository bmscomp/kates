package com.klster.kates.service;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.AdminClientConfig;
import org.apache.kafka.clients.admin.Config;
import org.apache.kafka.clients.admin.ConfigEntry;
import org.apache.kafka.clients.admin.ConsumerGroupDescription;
import org.apache.kafka.clients.admin.ConsumerGroupListing;
import org.apache.kafka.clients.admin.DescribeClusterResult;
import org.apache.kafka.clients.admin.ListOffsetsResult;
import org.apache.kafka.clients.admin.ListTopicsResult;
import org.apache.kafka.clients.admin.NewTopic;
import org.apache.kafka.clients.admin.OffsetSpec;
import org.apache.kafka.clients.admin.TopicDescription;
import org.apache.kafka.clients.consumer.OffsetAndMetadata;
import org.apache.kafka.common.Node;
import org.apache.kafka.common.TopicPartition;
import org.apache.kafka.common.TopicPartitionInfo;
import org.apache.kafka.common.config.ConfigResource;
import org.eclipse.microprofile.config.inject.ConfigProperty;

import java.util.ArrayList;
import java.util.Collection;
import java.util.Collections;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Properties;
import java.util.Set;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.logging.Level;
import java.util.logging.Logger;

@ApplicationScoped
public class KafkaAdminService {

    private static final Logger LOG = Logger.getLogger(KafkaAdminService.class.getName());
    private static final int TIMEOUT_SECONDS = 30;

    private final String bootstrapServers;

    @Inject
    public KafkaAdminService(
            @ConfigProperty(name = "kates.kafka.bootstrap-servers") String bootstrapServers) {
        this.bootstrapServers = bootstrapServers;
    }

    private AdminClient createClient() {
        Properties props = new Properties();
        props.put(AdminClientConfig.BOOTSTRAP_SERVERS_CONFIG, bootstrapServers);
        props.put(AdminClientConfig.REQUEST_TIMEOUT_MS_CONFIG, "15000");
        props.put(AdminClientConfig.DEFAULT_API_TIMEOUT_MS_CONFIG, "30000");
        return AdminClient.create(props);
    }

    public void createTopic(String name, int partitions, int replicationFactor, Map<String, String> configs) {
        try (AdminClient client = createClient()) {
            NewTopic newTopic = new NewTopic(name, partitions, (short) replicationFactor);
            if (configs != null && !configs.isEmpty()) {
                newTopic.configs(configs);
            }
            client.createTopics(Collections.singleton(newTopic))
                    .all()
                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            LOG.info("Created topic: " + name);
        } catch (ExecutionException e) {
            if (e.getCause() instanceof org.apache.kafka.common.errors.TopicExistsException) {
                LOG.info("Topic already exists: " + name);
            } else {
                throw new RuntimeException("Failed to create topic: " + name, e);
            }
        } catch (InterruptedException | TimeoutException e) {
            throw new RuntimeException("Failed to create topic: " + name, e);
        }
    }

    public void deleteTopic(String name) {
        try (AdminClient client = createClient()) {
            client.deleteTopics(Collections.singleton(name))
                    .all()
                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            LOG.info("Deleted topic: " + name);
        } catch (Exception e) {
            LOG.log(Level.WARNING, "Failed to delete topic: " + name, e);
        }
    }

    public Set<String> listTopics() {
        try (AdminClient client = createClient()) {
            ListTopicsResult result = client.listTopics();
            return result.names().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
        } catch (Exception e) {
            throw new RuntimeException("Failed to list topics", e);
        }
    }

    public Map<String, TopicDescription> describeTopics(Collection<String> topicNames) {
        try (AdminClient client = createClient()) {
            return client.describeTopics(topicNames)
                    .allTopicNames()
                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
        } catch (Exception e) {
            throw new RuntimeException("Failed to describe topics", e);
        }
    }

    public Map<String, Object> describeCluster() {
        try (AdminClient client = createClient()) {
            DescribeClusterResult cluster = client.describeCluster();
            Map<String, Object> info = new HashMap<>();
            info.put("clusterId", cluster.clusterId().get(TIMEOUT_SECONDS, TimeUnit.SECONDS));
            info.put("controller", nodeToMap(cluster.controller().get(TIMEOUT_SECONDS, TimeUnit.SECONDS)));

            Collection<Node> nodes = cluster.nodes().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            info.put("brokerCount", nodes.size());
            info.put("brokers", nodes.stream().map(this::nodeToMap).toList());

            return info;
        } catch (Exception e) {
            throw new RuntimeException("Failed to describe cluster", e);
        }
    }

    public boolean isReachable() {
        try (AdminClient client = createClient()) {
            client.describeCluster().clusterId().get(5, TimeUnit.SECONDS);
            return true;
        } catch (Exception e) {
            return false;
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

    public Map<String, Object> describeTopicDetail(String topicName) {
        try (AdminClient client = createClient()) {
            TopicDescription desc = client.describeTopics(Collections.singleton(topicName))
                    .allTopicNames()
                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS)
                    .get(topicName);

            if (desc == null) {
                throw new RuntimeException("Topic not found: " + topicName);
            }

            Map<String, Object> result = new LinkedHashMap<>();
            result.put("name", desc.name());
            result.put("internal", desc.isInternal());
            result.put("partitions", desc.partitions().size());

            int replicationFactor = desc.partitions().isEmpty()
                    ? 0
                    : desc.partitions().get(0).replicas().size();
            result.put("replicationFactor", replicationFactor);

            java.util.List<Map<String, Object>> partitionInfos = new java.util.ArrayList<>();
            for (TopicPartitionInfo pi : desc.partitions()) {
                Map<String, Object> pMap = new LinkedHashMap<>();
                pMap.put("partition", pi.partition());
                pMap.put("leader", pi.leader() != null ? pi.leader().id() : -1);
                pMap.put("replicas", pi.replicas().stream().map(Node::id).toList());
                pMap.put("isr", pi.isr().stream().map(Node::id).toList());
                pMap.put("underReplicated", pi.isr().size() < pi.replicas().size());
                partitionInfos.add(pMap);
            }
            result.put("partitionInfo", partitionInfos);

            ConfigResource resource = new ConfigResource(ConfigResource.Type.TOPIC, topicName);
            Config config = client.describeConfigs(Collections.singleton(resource))
                    .all()
                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS)
                    .get(resource);

            Map<String, String> configs = new LinkedHashMap<>();
            for (ConfigEntry entry : config.entries()) {
                if (entry.source() == ConfigEntry.ConfigSource.DYNAMIC_TOPIC_CONFIG
                        || entry.source() == ConfigEntry.ConfigSource.DEFAULT_CONFIG) {
                    switch (entry.name()) {
                        case "cleanup.policy", "retention.ms", "retention.bytes",
                             "min.insync.replicas", "compression.type", "segment.bytes",
                             "max.message.bytes", "message.timestamp.type" ->
                                configs.put(entry.name(), entry.value());
                    }
                }
            }
            result.put("configs", configs);
            return result;

        } catch (Exception e) {
            throw new RuntimeException("Failed to describe topic: " + topicName, e);
        }
    }

    public List<Map<String, Object>> listConsumerGroups() {
        try (AdminClient client = createClient()) {
            Collection<ConsumerGroupListing> groups = client.listConsumerGroups()
                    .all()
                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS);

            List<String> groupIds = groups.stream()
                    .map(ConsumerGroupListing::groupId)
                    .toList();

            Map<String, ConsumerGroupDescription> descriptions = groupIds.isEmpty()
                    ? Map.of()
                    : client.describeConsumerGroups(groupIds)
                            .all()
                            .get(TIMEOUT_SECONDS, TimeUnit.SECONDS);

            List<Map<String, Object>> result = new ArrayList<>();
            for (ConsumerGroupListing listing : groups) {
                Map<String, Object> item = new LinkedHashMap<>();
                item.put("groupId", listing.groupId());
                ConsumerGroupDescription desc = descriptions.get(listing.groupId());
                item.put("state", desc != null ? desc.state().toString() : "UNKNOWN");
                item.put("members", desc != null ? desc.members().size() : 0);
                result.add(item);
            }
            return result;
        } catch (Exception e) {
            throw new RuntimeException("Failed to list consumer groups", e);
        }
    }

    public Map<String, Object> describeConsumerGroup(String groupId) {
        try (AdminClient client = createClient()) {
            ConsumerGroupDescription desc = client.describeConsumerGroups(Collections.singleton(groupId))
                    .all()
                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS)
                    .get(groupId);

            if (desc == null) {
                throw new RuntimeException("Consumer group not found: " + groupId);
            }

            Map<String, Object> result = new LinkedHashMap<>();
            result.put("groupId", desc.groupId());
            result.put("state", desc.state().toString());
            result.put("members", desc.members().size());

            Map<TopicPartition, OffsetAndMetadata> offsets = client
                    .listConsumerGroupOffsets(groupId)
                    .partitionsToOffsetAndMetadata()
                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS);

            Map<TopicPartition, OffsetSpec> latestRequest = new HashMap<>();
            offsets.keySet().forEach(tp -> latestRequest.put(tp, OffsetSpec.latest()));

            Map<TopicPartition, ListOffsetsResult.ListOffsetsResultInfo> endOffsets =
                    latestRequest.isEmpty()
                            ? Map.of()
                            : client.listOffsets(latestRequest)
                                    .all()
                                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS);

            List<Map<String, Object>> offsetList = new ArrayList<>();
            long totalLag = 0;

            for (var entry : offsets.entrySet()) {
                TopicPartition tp = entry.getKey();
                long current = entry.getValue().offset();
                long end = endOffsets.containsKey(tp)
                        ? endOffsets.get(tp).offset()
                        : current;
                long lag = Math.max(0, end - current);
                totalLag += lag;

                Map<String, Object> item = new LinkedHashMap<>();
                item.put("topic", tp.topic());
                item.put("partition", tp.partition());
                item.put("currentOffset", current);
                item.put("endOffset", end);
                item.put("lag", lag);
                offsetList.add(item);
            }

            result.put("offsets", offsetList);
            result.put("totalLag", totalLag);
            return result;

        } catch (Exception e) {
            throw new RuntimeException("Failed to describe consumer group: " + groupId, e);
        }
    }
}
