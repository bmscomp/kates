package com.bmscomp.kates.service;

import java.util.Collection;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.TimeUnit;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.AlterConfigOp;
import org.apache.kafka.clients.admin.Config;
import org.apache.kafka.clients.admin.ConfigEntry;
import org.apache.kafka.clients.admin.ListTopicsResult;
import org.apache.kafka.clients.admin.NewTopic;
import org.apache.kafka.clients.admin.TopicDescription;
import org.apache.kafka.common.Node;
import org.apache.kafka.common.TopicPartitionInfo;
import org.apache.kafka.common.config.ConfigResource;
import org.jboss.logging.Logger;

@ApplicationScoped
public class TopicService {

    private static final Logger LOG = Logger.getLogger(TopicService.class);
    private static final int TIMEOUT_SECONDS = 30;
    private static final long CACHE_TTL_MS = 30_000;

    private final KafkaAdminService adminService;

    private volatile Set<String> cachedTopics;
    private volatile long topicsCacheExpiry;

    @Inject
    public TopicService(KafkaAdminService adminService) {
        this.adminService = adminService;
    }

    public void evictCache() {
        topicsCacheExpiry = 0;
        cachedTopics = null;
    }

    public void createTopic(String name, int partitions, int replicationFactor, Map<String, String> configs) {
        AdminClient client = adminService.getClient();
        try {
            NewTopic newTopic = new NewTopic(name, partitions, (short) replicationFactor);
            if (configs != null && !configs.isEmpty()) {
                newTopic.configs(configs);
            }
            client.createTopics(Collections.singleton(newTopic)).all().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            LOG.info("Created topic: " + name);
        } catch (java.util.concurrent.ExecutionException e) {
            if (e.getCause() instanceof org.apache.kafka.common.errors.TopicExistsException) {
                LOG.info("Topic already exists: " + name);
            } else {
                throw new RuntimeException("Failed to create topic: " + name, e);
            }
        } catch (InterruptedException | java.util.concurrent.TimeoutException e) {
            throw new RuntimeException("Failed to create topic: " + name, e);
        }
    }

    public void deleteTopic(String name) {
        AdminClient client = adminService.getClient();
        try {
            client.deleteTopics(Collections.singleton(name)).all().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            LOG.info("Deleted topic: " + name);
        } catch (Exception e) {
            throw new RuntimeException("Failed to delete topic: " + name, e);
        }
    }

    public void alterTopicConfig(String name, Map<String, String> configs) {
        AdminClient client = adminService.getClient();
        try {
            ConfigResource resource = new ConfigResource(ConfigResource.Type.TOPIC, name);
            List<AlterConfigOp> ops = configs.entrySet().stream()
                    .map(e -> new AlterConfigOp(
                            new ConfigEntry(e.getKey(), e.getValue()),
                            AlterConfigOp.OpType.SET))
                    .toList();
            client.incrementalAlterConfigs(Map.of(resource, ops)).all().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            LOG.info("Altered config for topic: " + name);
        } catch (Exception e) {
            throw new RuntimeException("Failed to alter config for topic: " + name, e);
        }
    }

    public Set<String> listTopics() {
        if (cachedTopics != null && System.currentTimeMillis() < topicsCacheExpiry) {
            return cachedTopics;
        }
        try {
            AdminClient client = adminService.getClient();
            ListTopicsResult result = client.listTopics();
            Set<String> topics = result.names().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            cachedTopics = topics;
            topicsCacheExpiry = System.currentTimeMillis() + CACHE_TTL_MS;
            return topics;
        } catch (Exception e) {
            throw new RuntimeException("Failed to list topics", e);
        }
    }

    public Map<String, TopicDescription> describeTopics(Collection<String> topicNames) {
        AdminClient client = adminService.getClient();
        try {
            return client.describeTopics(topicNames).allTopicNames().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
        } catch (Exception e) {
            throw new RuntimeException("Failed to describe topics", e);
        }
    }

    public Map<String, Object> describeTopicDetail(String topicName) {
        AdminClient client = adminService.getClient();
        try {
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

            Map<String, String> topicConfigs = new LinkedHashMap<>();
            for (ConfigEntry entry : config.entries()) {
                if (entry.source() == ConfigEntry.ConfigSource.DYNAMIC_TOPIC_CONFIG
                        || entry.source() == ConfigEntry.ConfigSource.DEFAULT_CONFIG) {
                    switch (entry.name()) {
                        case "cleanup.policy",
                                "retention.ms",
                                "retention.bytes",
                                "min.insync.replicas",
                                "compression.type",
                                "segment.bytes",
                                "max.message.bytes",
                                "message.timestamp.type" -> topicConfigs.put(entry.name(), entry.value());
                    }
                }
            }
            result.put("configs", topicConfigs);
            return result;

        } catch (Exception e) {
            throw new RuntimeException("Failed to describe topic: " + topicName, e);
        }
    }
}
