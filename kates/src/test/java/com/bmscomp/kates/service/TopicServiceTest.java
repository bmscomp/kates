package com.bmscomp.kates.service;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import java.util.List;
import java.util.Map;
import java.util.Set;
import jakarta.inject.Inject;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.Config;
import org.apache.kafka.clients.admin.ConfigEntry;
import org.apache.kafka.clients.admin.CreateTopicsResult;
import org.apache.kafka.clients.admin.DeleteTopicsResult;
import org.apache.kafka.clients.admin.DescribeConfigsResult;
import org.apache.kafka.clients.admin.DescribeTopicsResult;
import org.apache.kafka.clients.admin.ListTopicsResult;
import org.apache.kafka.clients.admin.TopicDescription;
import org.apache.kafka.common.KafkaFuture;
import org.apache.kafka.common.Node;
import org.apache.kafka.common.TopicPartitionInfo;
import org.apache.kafka.common.config.ConfigResource;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

@QuarkusTest
class TopicServiceTest {

    @InjectMock
    KafkaAdminService kafkaAdminService;

    @Inject
    TopicService topicService;

    AdminClient mockClient;

    @BeforeEach
    void setUp() {
        mockClient = mock(AdminClient.class);
        when(kafkaAdminService.getClient()).thenReturn(mockClient);
        topicService.evictCache();
    }

    @Test
    void createTopicSucceeds() {
        CreateTopicsResult result = mock(CreateTopicsResult.class);
        when(result.all()).thenReturn(KafkaFuture.completedFuture(null));
        when(mockClient.createTopics(any())).thenReturn(result);

        assertDoesNotThrow(() -> topicService.createTopic("test-topic", 3, 1, Map.of("retention.ms", "60000")));
    }

    @Test
    void createTopicWithNullConfigsSucceeds() {
        CreateTopicsResult result = mock(CreateTopicsResult.class);
        when(result.all()).thenReturn(KafkaFuture.completedFuture(null));
        when(mockClient.createTopics(any())).thenReturn(result);

        assertDoesNotThrow(() -> topicService.createTopic("test-topic", 1, 1, null));
    }

    @Test
    void deleteTopicSucceeds() {
        DeleteTopicsResult result = mock(DeleteTopicsResult.class);
        when(result.all()).thenReturn(KafkaFuture.completedFuture(null));
        when(mockClient.deleteTopics(any(java.util.Collection.class))).thenReturn(result);

        assertDoesNotThrow(() -> topicService.deleteTopic("test-topic"));
    }

    @Test
    void listTopicsReturnsFromClient() {
        ListTopicsResult result = mock(ListTopicsResult.class);
        when(result.names()).thenReturn(KafkaFuture.completedFuture(Set.of("topic-a", "topic-b")));
        when(mockClient.listTopics()).thenReturn(result);

        Set<String> topics = topicService.listTopics();
        assertEquals(2, topics.size());
        assertTrue(topics.contains("topic-a"));
        assertTrue(topics.contains("topic-b"));
    }

    @Test
    void listTopicsCacheWorks() {
        ListTopicsResult result = mock(ListTopicsResult.class);
        when(result.names()).thenReturn(KafkaFuture.completedFuture(Set.of("topic-a")));
        when(mockClient.listTopics()).thenReturn(result);

        topicService.listTopics();
        topicService.listTopics();

        verify(mockClient, times(1)).listTopics();
    }

    @Test
    void evictCacheForcesRefresh() {
        ListTopicsResult result = mock(ListTopicsResult.class);
        when(result.names()).thenReturn(KafkaFuture.completedFuture(Set.of("topic-a")));
        when(mockClient.listTopics()).thenReturn(result);

        topicService.listTopics();
        topicService.evictCache();
        topicService.listTopics();

        verify(mockClient, times(2)).listTopics();
    }

    @Test
    void topicDetailReportsEachKeyInForceWithWhereItComesFrom() {
        // The kafka-cluster chart sets min.insync.replicas, retention and
        // cleanup at broker level. Topic detail kept only entries set on the
        // topic or left at Kafka's default, so on that cluster those keys
        // were simply missing, and a missing min.insync.replicas read as 1.
        Node broker = new Node(0, "b0", 9092);
        TopicDescription description = new TopicDescription(
                "orders", false, List.of(new TopicPartitionInfo(0, broker, List.of(broker), List.of(broker))));
        DescribeTopicsResult topics = mock(DescribeTopicsResult.class);
        when(topics.allTopicNames()).thenReturn(KafkaFuture.completedFuture(Map.of("orders", description)));
        when(mockClient.describeTopics(anyCollection())).thenReturn(topics);

        ConfigResource resource = new ConfigResource(ConfigResource.Type.TOPIC, "orders");
        Config config = new Config(List.of(
                entry("min.insync.replicas", "2", ConfigEntry.ConfigSource.STATIC_BROKER_CONFIG),
                entry("retention.ms", "600000", ConfigEntry.ConfigSource.DYNAMIC_TOPIC_CONFIG),
                entry("cleanup.policy", "delete", ConfigEntry.ConfigSource.DYNAMIC_DEFAULT_BROKER_CONFIG),
                entry("segment.bytes", "1073741824", ConfigEntry.ConfigSource.DYNAMIC_BROKER_CONFIG),
                entry("compression.type", "producer", ConfigEntry.ConfigSource.DEFAULT_CONFIG),
                // Not one of the keys topic detail reports, whatever its source.
                entry("unclean.leader.election.enable", "false", ConfigEntry.ConfigSource.STATIC_BROKER_CONFIG)));
        DescribeConfigsResult configs = mock(DescribeConfigsResult.class);
        when(configs.all()).thenReturn(KafkaFuture.completedFuture(Map.of(resource, config)));
        when(mockClient.describeConfigs(anyCollection())).thenReturn(configs);

        Map<String, Object> detail = topicService.describeTopicDetail("orders");

        assertEquals(
                Map.of(
                        "min.insync.replicas", "2",
                        "retention.ms", "600000",
                        "cleanup.policy", "delete",
                        "segment.bytes", "1073741824",
                        "compression.type", "producer"),
                detail.get("configs"));
        assertEquals(
                Map.of(
                        "min.insync.replicas", "STATIC_BROKER_CONFIG",
                        "retention.ms", "DYNAMIC_TOPIC_CONFIG",
                        "cleanup.policy", "DYNAMIC_DEFAULT_BROKER_CONFIG",
                        "segment.bytes", "DYNAMIC_BROKER_CONFIG",
                        "compression.type", "DEFAULT_CONFIG"),
                detail.get("configSources"));
    }

    private static ConfigEntry entry(String name, String value, ConfigEntry.ConfigSource source) {
        return new ConfigEntry(name, value, source, false, false, List.of(), ConfigEntry.ConfigType.STRING, null);
    }
}
