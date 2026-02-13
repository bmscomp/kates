package com.klster.kates.service;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.AdminClientConfig;
import org.apache.kafka.clients.admin.DescribeClusterResult;
import org.apache.kafka.clients.admin.ListTopicsResult;
import org.apache.kafka.clients.admin.NewTopic;
import org.apache.kafka.clients.admin.TopicDescription;
import org.apache.kafka.common.Node;
import org.eclipse.microprofile.config.inject.ConfigProperty;

import java.util.Collection;
import java.util.Collections;
import java.util.HashMap;
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
}
