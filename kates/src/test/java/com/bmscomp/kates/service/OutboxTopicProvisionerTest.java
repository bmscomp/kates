package com.bmscomp.kates.service;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.anyCollection;
import static org.mockito.Mockito.*;

import java.util.Collection;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.stream.IntStream;

import io.smallrye.config.PropertiesConfigSource;
import io.smallrye.config.SmallRyeConfigBuilder;
import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.CreateTopicsResult;
import org.apache.kafka.clients.admin.DescribeClusterResult;
import org.apache.kafka.clients.admin.DescribeTopicsResult;
import org.apache.kafka.clients.admin.NewTopic;
import org.apache.kafka.clients.admin.TopicDescription;
import org.apache.kafka.common.KafkaFuture;
import org.apache.kafka.common.Node;
import org.apache.kafka.common.errors.TimeoutException;
import org.apache.kafka.common.errors.TopicAuthorizationException;
import org.apache.kafka.common.errors.TopicExistsException;
import org.apache.kafka.common.errors.UnknownTopicOrPartitionException;
import org.apache.kafka.common.internals.KafkaFutureImpl;
import org.eclipse.microprofile.config.Config;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;

/**
 * The outbox published every lifecycle event to {@code kates-test-events}, a
 * topic nothing created on a cluster that refuses to auto-create one, so every
 * event was dead-lettered and no webhook fired. These pin how the provisioner
 * decides what to create, what it creates, and that no Kafka failure escapes
 * the scheduler.
 */
class OutboxTopicProvisionerTest {

    private static final String BROKER = "krafter-kafka-bootstrap.kafka.svc:9092";
    private static final String TOPIC = "kates-test-events";

    private KafkaAdminService adminService;
    private AdminClient admin;

    @BeforeEach
    void setUp() {
        adminService = mock(KafkaAdminService.class);
        admin = mock(AdminClient.class);
        when(adminService.sharedAdminClient()).thenReturn(admin);
    }

    /** The channel wiring in application.properties. */
    private static Map<String, String> productionChannels() {
        Map<String, String> properties = new HashMap<>();
        properties.put("mp.messaging.outgoing.test-events-out.connector", "smallrye-kafka");
        properties.put("mp.messaging.outgoing.test-events-out.topic", TOPIC);
        properties.put("mp.messaging.incoming.test-events-in.connector", "smallrye-kafka");
        properties.put("mp.messaging.incoming.test-events-in.topic", TOPIC);
        return properties;
    }

    private static Config config(Map<String, String> properties) {
        return new SmallRyeConfigBuilder()
                .withSources(new PropertiesConfigSource(properties, "test", 100))
                .build();
    }

    private OutboxTopicProvisioner provisioner(Map<String, String> properties) {
        return new OutboxTopicProvisioner(adminService, config(properties), BROKER, 1, 3, "604800000");
    }

    private void topicExists(boolean exists) {
        KafkaFutureImpl<TopicDescription> description = new KafkaFutureImpl<>();
        if (exists) {
            description.complete(mock(TopicDescription.class));
        } else {
            description.completeExceptionally(new UnknownTopicOrPartitionException("missing"));
        }
        DescribeTopicsResult result = mock(DescribeTopicsResult.class);
        when(result.topicNameValues()).thenReturn(Map.of(TOPIC, description));
        when(admin.describeTopics(anyCollection())).thenReturn(result);
    }

    private void brokers(int count) {
        List<Node> nodes = IntStream.range(0, count)
                .mapToObj(id -> new Node(id, "broker-" + id, 9092))
                .toList();
        DescribeClusterResult result = mock(DescribeClusterResult.class);
        when(result.nodes()).thenReturn(KafkaFuture.<Collection<Node>>completedFuture(nodes));
        when(admin.describeCluster()).thenReturn(result);
    }

    private void createReturns(KafkaFuture<Void> outcome) {
        CreateTopicsResult result = mock(CreateTopicsResult.class);
        when(result.values()).thenReturn(Map.of(TOPIC, outcome));
        when(admin.createTopics(anyCollection())).thenReturn(result);
    }

    private static KafkaFuture<Void> failed(Throwable cause) {
        KafkaFutureImpl<Void> future = new KafkaFutureImpl<>();
        future.completeExceptionally(cause);
        return future;
    }

    @SuppressWarnings("unchecked")
    private NewTopic createdTopic() {
        ArgumentCaptor<Collection<NewTopic>> captor = ArgumentCaptor.forClass(Collection.class);
        verify(admin).createTopics(captor.capture());
        assertEquals(1, captor.getValue().size(), "one topic backs both channels");
        return captor.getValue().iterator().next();
    }

    @Test
    void bothChannelsShareTheOneTopicTheyName() {
        assertEquals(Set.of(TOPIC), OutboxTopicProvisioner.topicsToEnsure(config(productionChannels()), BROKER));
    }

    @Test
    void channelsOffTheKafkaConnectorNeedNoTopic() {
        Map<String, String> properties = productionChannels();
        properties.put("mp.messaging.outgoing.test-events-out.connector", "smallrye-in-memory");
        properties.put("mp.messaging.incoming.test-events-in.connector", "smallrye-in-memory");

        assertTrue(OutboxTopicProvisioner.topicsToEnsure(config(properties), BROKER)
                .isEmpty());
    }

    @Test
    void aChannelWithoutATopicUsesItsChannelName() {
        Map<String, String> properties = productionChannels();
        properties.remove("mp.messaging.incoming.test-events-in.topic");

        assertEquals(
                Set.of(TOPIC, "test-events-in"),
                OutboxTopicProvisioner.topicsToEnsure(config(properties), BROKER),
                "the connector falls back to the channel name, so that is the topic it reads");
    }

    @Test
    void aChannelOnAnotherClusterIsLeftToThatCluster() {
        Map<String, String> properties = productionChannels();
        properties.put("kafka.bootstrap.servers", "localhost:32771");

        assertTrue(
                OutboxTopicProvisioner.topicsToEnsure(config(properties), BROKER)
                        .isEmpty(),
                "the shared AdminClient would create the topic on the wrong cluster");

        properties.put("kafka.bootstrap.servers", BROKER);
        assertEquals(Set.of(TOPIC), OutboxTopicProvisioner.topicsToEnsure(config(properties), BROKER));
    }

    @Test
    void aMissingTopicIsCreatedForItsUse() {
        topicExists(false);
        brokers(3);
        createReturns(KafkaFuture.completedFuture(null));

        assertEquals(Set.of(TOPIC), provisioner(productionChannels()).ensureTopics());

        NewTopic topic = createdTopic();
        assertEquals(TOPIC, topic.name());
        assertEquals(1, topic.numPartitions());
        assertEquals(3, topic.replicationFactor());
        assertEquals(Map.of("cleanup.policy", "delete", "retention.ms", "604800000"), topic.configs());
    }

    @Test
    void theReplicationFactorIsCappedAtTheBrokerCount() {
        topicExists(false);
        brokers(1);
        createReturns(KafkaFuture.completedFuture(null));

        provisioner(productionChannels()).ensureTopics();

        assertEquals(1, createdTopic().replicationFactor(), "a one-broker cluster rejects replication factor 3");
    }

    @Test
    void anExistingTopicIsLeftAsItIs() {
        topicExists(true);

        assertTrue(provisioner(productionChannels()).ensureTopics().isEmpty());

        verify(admin, never()).createTopics(anyCollection());
    }

    @Test
    void losingTheCreateRaceToAnotherReplicaIsNotAFailure() {
        topicExists(false);
        brokers(3);
        createReturns(failed(new TopicExistsException("created by another replica")));

        assertTrue(assertDoesNotThrow(() -> provisioner(productionChannels()).ensureTopics())
                .isEmpty());
    }

    @Test
    void aPrincipalThatMayNotCreateTopicsFailsQuietly() {
        topicExists(false);
        brokers(3);
        createReturns(failed(new TopicAuthorizationException("denied")));

        assertTrue(assertDoesNotThrow(() -> provisioner(productionChannels()).ensureTopics())
                .isEmpty());
    }

    @Test
    void anUnreachableClusterFailsQuietlyAndCreatesNothing() {
        KafkaFutureImpl<TopicDescription> description = new KafkaFutureImpl<>();
        description.completeExceptionally(new TimeoutException("Timed out waiting for a node assignment"));
        DescribeTopicsResult result = mock(DescribeTopicsResult.class);
        when(result.topicNameValues()).thenReturn(Map.of(TOPIC, description));
        when(admin.describeTopics(anyCollection())).thenReturn(result);

        assertTrue(assertDoesNotThrow(() -> provisioner(productionChannels()).ensureTopics())
                .isEmpty());

        verify(admin, never()).createTopics(anyCollection());
    }

    @Test
    void withoutKafkaChannelsTheClusterIsNeverAsked() {
        Map<String, String> properties = productionChannels();
        properties.put("mp.messaging.outgoing.test-events-out.connector", "smallrye-in-memory");
        properties.put("mp.messaging.incoming.test-events-in.connector", "smallrye-in-memory");

        provisioner(properties).ensureTopics();

        verifyNoInteractions(adminService);
    }
}
