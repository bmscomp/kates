package com.bmscomp.kates.service;

import java.util.Collection;
import java.util.List;
import java.util.Map;
import java.util.Properties;
import java.util.Set;
import java.util.concurrent.locks.ReentrantLock;

import jakarta.annotation.PostConstruct;
import jakarta.annotation.PreDestroy;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.AdminClientConfig;
import org.apache.kafka.clients.admin.TopicDescription;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import com.bmscomp.kates.report.ClusterSnapshot;

/**
 * Manages the shared Kafka AdminClient lifecycle.
 * Business operations are delegated to focused services:
 * {@link TopicService}, {@link ConsumerGroupService},
 * {@link ClusterHealthService}, {@link KafkaClientService}.
 */
@ApplicationScoped
public class KafkaAdminService {

    private static final Logger LOG = Logger.getLogger(KafkaAdminService.class);

    private final String bootstrapServers;
    private volatile AdminClient sharedClient;
    private final ReentrantLock clientLock = new ReentrantLock();

    @Inject
    TopicService topicService;

    @Inject
    ConsumerGroupService consumerGroupService;

    @Inject
    ClusterHealthService clusterHealthService;

    @Inject
    KafkaClientService kafkaClientService;

    @Inject
    public KafkaAdminService(@ConfigProperty(name = "kates.kafka.bootstrap-servers") String bootstrapServers) {
        this.bootstrapServers = bootstrapServers;
    }

    @PostConstruct
    void init() {
        try {
            sharedClient = buildClient();
            LOG.info("AdminClient pool initialized for: " + bootstrapServers);
        } catch (Exception e) {
            LOG.warn("AdminClient init deferred — broker not reachable: " + e.getMessage());
        }
    }

    @PreDestroy
    void shutdown() {
        if (sharedClient != null) {
            try {
                sharedClient.close(java.time.Duration.ofSeconds(5));
                LOG.info("AdminClient pool closed");
            } catch (Exception e) {
                LOG.warn("AdminClient close failed", e);
            }
        }
    }

    private AdminClient buildClient() {
        Properties props = new Properties();
        props.put(AdminClientConfig.BOOTSTRAP_SERVERS_CONFIG, bootstrapServers);
        props.put(AdminClientConfig.REQUEST_TIMEOUT_MS_CONFIG, "15000");
        props.put(AdminClientConfig.DEFAULT_API_TIMEOUT_MS_CONFIG, "30000");
        props.put(AdminClientConfig.METRIC_REPORTER_CLASSES_CONFIG, "");
        return AdminClient.create(props);
    }

    AdminClient getClient() {
        AdminClient c = sharedClient;
        if (c != null) return c;
        clientLock.lock();
        try {
            if (sharedClient == null) {
                sharedClient = buildClient();
            }
            return sharedClient;
        } finally {
            clientLock.unlock();
        }
    }

    public String getBootstrapServers() {
        return bootstrapServers;
    }

    public void evictCache() {
        topicService.evictCache();
        clusterHealthService.evictCache();
    }

    @Deprecated(forRemoval = true)
    public void createTopic(String name, int partitions, int replicationFactor, Map<String, String> configs) {
        topicService.createTopic(name, partitions, replicationFactor, configs);
    }

    @Deprecated(forRemoval = true)
    public void deleteTopic(String name) {
        topicService.deleteTopic(name);
    }

    @Deprecated(forRemoval = true)
    public void alterTopicConfig(String name, Map<String, String> configs) {
        topicService.alterTopicConfig(name, configs);
    }

    @Deprecated(forRemoval = true)
    public Set<String> listTopics() {
        return topicService.listTopics();
    }

    @Deprecated(forRemoval = true)
    public Map<String, TopicDescription> describeTopics(Collection<String> topicNames) {
        return topicService.describeTopics(topicNames);
    }

    @Deprecated(forRemoval = true)
    public Map<String, Object> describeTopicDetail(String topicName) {
        return topicService.describeTopicDetail(topicName);
    }

    @Deprecated(forRemoval = true)
    public List<Map<String, Object>> listConsumerGroups() {
        return consumerGroupService.listConsumerGroups();
    }

    @Deprecated(forRemoval = true)
    public Map<String, Object> describeConsumerGroup(String groupId) {
        return consumerGroupService.describeConsumerGroup(groupId);
    }

    @Deprecated(forRemoval = true)
    public Map<String, Object> describeCluster() {
        return clusterHealthService.describeCluster();
    }

    @Deprecated(forRemoval = true)
    public boolean isReachable() {
        return clusterHealthService.isReachable();
    }

    @Deprecated(forRemoval = true)
    public int brokerCount() {
        return clusterHealthService.brokerCount();
    }

    @Deprecated(forRemoval = true)
    public List<Map<String, Object>> describeBrokerConfigs(int brokerId) {
        return clusterHealthService.describeBrokerConfigs(brokerId);
    }

    @Deprecated(forRemoval = true)
    public ClusterSnapshot captureSnapshot(String topicName) {
        return clusterHealthService.captureSnapshot(topicName);
    }

    @Deprecated(forRemoval = true)
    public Map<String, Object> clusterHealthCheck() {
        return clusterHealthService.clusterHealthCheck();
    }

    @Deprecated(forRemoval = true)
    public Map<String, Object> produceRecord(String topic, String key, String value) {
        return kafkaClientService.produceRecord(topic, key, value);
    }

    @Deprecated(forRemoval = true)
    public List<Map<String, Object>> fetchRecords(String topic, String offsetReset, int limit) {
        return kafkaClientService.fetchRecords(topic, offsetReset, limit);
    }
}
