package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyCollection;
import static org.mockito.ArgumentMatchers.anyMap;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

import java.time.Duration;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Properties;
import java.util.Set;
import java.util.concurrent.atomic.AtomicReference;

import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.ConsumerGroupListing;
import org.apache.kafka.clients.admin.DescribeClusterOptions;
import org.apache.kafka.clients.admin.DescribeClusterResult;
import org.apache.kafka.clients.admin.DescribeTopicsOptions;
import org.apache.kafka.clients.admin.DescribeTopicsResult;
import org.apache.kafka.clients.admin.ListConsumerGroupOffsetsOptions;
import org.apache.kafka.clients.admin.ListConsumerGroupOffsetsResult;
import org.apache.kafka.clients.admin.ListConsumerGroupOffsetsSpec;
import org.apache.kafka.clients.admin.ListConsumerGroupsOptions;
import org.apache.kafka.clients.admin.ListConsumerGroupsResult;
import org.apache.kafka.clients.admin.ListOffsetsOptions;
import org.apache.kafka.clients.admin.ListOffsetsResult;
import org.apache.kafka.clients.admin.ListOffsetsResult.ListOffsetsResultInfo;
import org.apache.kafka.clients.admin.ListPartitionReassignmentsOptions;
import org.apache.kafka.clients.admin.ListPartitionReassignmentsResult;
import org.apache.kafka.clients.admin.ListTopicsOptions;
import org.apache.kafka.clients.admin.ListTopicsResult;
import org.apache.kafka.clients.admin.PartitionReassignment;
import org.apache.kafka.clients.admin.TopicDescription;
import org.apache.kafka.clients.consumer.OffsetAndMetadata;
import org.apache.kafka.clients.producer.MockProducer;
import org.apache.kafka.clients.producer.ProducerConfig;
import org.apache.kafka.common.KafkaFuture;
import org.apache.kafka.common.Node;
import org.apache.kafka.common.TopicPartition;
import org.apache.kafka.common.TopicPartitionInfo;
import org.apache.kafka.common.errors.NotEnoughReplicasException;
import org.apache.kafka.common.errors.TimeoutException;
import org.apache.kafka.common.internals.KafkaFutureImpl;
import org.apache.kafka.common.serialization.ByteArraySerializer;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;

import com.bmscomp.kates.config.KafkaSecurityConfig;
import com.bmscomp.kates.service.KafkaAdminService;

/**
 * The kafkaProbe checks count as kafka-topics.sh and kafka-consumer-groups.sh
 * do, and fail when they cannot ask: the CLI pipelines they replace printed 0
 * then, which passed.
 */
class KafkaProbeChecksTest {

    private static final Duration TIMEOUT = Duration.ofSeconds(10);
    private static final Node B0 = new Node(0, "b0", 9092);
    private static final Node B1 = new Node(1, "b1", 9092);
    private static final Node B2 = new Node(2, "b2", 9092);
    private static final Node B3 = new Node(3, "b3", 9092);

    private AdminClient admin;
    private KafkaSecurityConfig securityConfig;
    private KafkaProbeChecks checks;

    @BeforeEach
    void setup() {
        admin = mock(AdminClient.class);
        securityConfig = mock(KafkaSecurityConfig.class);
        KafkaAdminService adminService = mock(KafkaAdminService.class);
        when(adminService.sharedAdminClient()).thenReturn(admin);
        when(adminService.getBootstrapServers()).thenReturn("krafter-kafka-bootstrap.kafka.svc:9092");
        checks = new KafkaProbeChecks();
        checks.adminService = adminService;
        checks.securityConfig = securityConfig;
    }

    @Test
    void underReplicatedCountsThePartitionsShortOfInSyncReplicasInternalTopicsIncluded() {
        topics(
                new TopicDescription(
                        "orders",
                        false,
                        List.of(
                                partition(0, B0, List.of(B0, B1, B2), List.of(B0, B1, B2)),
                                partition(1, B1, List.of(B0, B1, B2), List.of(B1, B2)))),
                new TopicDescription(
                        "__consumer_offsets", true, List.of(partition(0, B2, List.of(B0, B1, B2), List.of(B2)))));
        reassignments(Map.of());

        assertEquals("2", checks.run("under-replicated-partitions", TIMEOUT));

        ArgumentCaptor<ListTopicsOptions> options = ArgumentCaptor.forClass(ListTopicsOptions.class);
        verify(admin).listTopics(options.capture());
        assertTrue(options.getValue().shouldListInternal(), "kafka-topics.sh --describe lists internal topics");
    }

    @Test
    void aReplicaStillBeingAddedIsNotExpectedInSync() {
        topics(new TopicDescription(
                "orders",
                false,
                List.of(
                        // Moving from 0,1,2 to 1,2,3: 3 is still catching up.
                        partition(0, B1, List.of(B0, B1, B2, B3), List.of(B0, B1, B2)),
                        // Its reassignment is over: 0 and 3 are gone from its replicas.
                        partition(1, B1, List.of(B1, B2), List.of(B1)))));
        reassignments(Map.of(
                new TopicPartition("orders", 0), new PartitionReassignment(List.of(0, 1, 2, 3), List.of(3), List.of(0)),
                new TopicPartition("orders", 1),
                        new PartitionReassignment(List.of(0, 1, 2, 3), List.of(3), List.of(0))));

        assertEquals("1", checks.run("under-replicated-partitions", TIMEOUT));
    }

    @Test
    void unavailableCountsPartitionsWithoutALeaderOrWithOneThatIsNotALiveBroker() {
        topics(new TopicDescription(
                "orders",
                false,
                List.of(
                        partition(0, null, List.of(B0, B1, B2), List.of()),
                        partition(1, B3, List.of(B1, B2, B3), List.of(B3)),
                        partition(2, B1, List.of(B0, B1, B2), List.of(B0, B1, B2)))));
        DescribeClusterResult cluster = mock(DescribeClusterResult.class);
        when(cluster.nodes()).thenReturn(KafkaFuture.completedFuture(List.of(B0, B1, B2)));
        when(admin.describeCluster(any(DescribeClusterOptions.class))).thenReturn(cluster);

        assertEquals("2", checks.run("unavailable-partitions", TIMEOUT));
    }

    @Test
    void aCheckThatCannotReachTheClusterFails() {
        ListTopicsResult listed = mock(ListTopicsResult.class);
        KafkaFutureImpl<Set<String>> names = new KafkaFutureImpl<>();
        names.completeExceptionally(new TimeoutException("Timed out waiting for a node assignment. Call: listTopics"));
        when(listed.names()).thenReturn(names);
        when(admin.listTopics(any(ListTopicsOptions.class))).thenReturn(listed);

        ProbeFailure failure =
                assertThrows(ProbeFailure.class, () -> checks.run("under-replicated-partitions", TIMEOUT));

        assertEquals(
                "listing topics failed: TimeoutException: Timed out waiting for a node assignment. Call: listTopics",
                failure.getMessage());
    }

    @Test
    void aCheckThatOutlastsTheProbesTimeoutFails() {
        ListTopicsResult listed = mock(ListTopicsResult.class);
        when(listed.names()).thenReturn(new KafkaFutureImpl<>());
        when(admin.listTopics(any(ListTopicsOptions.class))).thenReturn(listed);

        ProbeFailure failure =
                assertThrows(ProbeFailure.class, () -> checks.run("unavailable-partitions", Duration.ofMillis(200)));

        assertEquals("listing topics did not finish within 200 ms", failure.getMessage());
    }

    @Test
    void produceSendsItsRecordsWithAcksAllAndPrintsTheAcknowledgedRate() {
        MockProducer<byte[], byte[]> producer = producer(true);
        AtomicReference<Properties> config = new AtomicReference<>();
        checks.producerFactory = props -> {
            config.set(props);
            return producer;
        };

        String rate = checks.run("produce kates-probe-topic", Duration.ofSeconds(30));

        assertTrue(Double.parseDouble(rate) > 0, rate);
        assertEquals(KafkaProbeChecks.PRODUCE_RECORDS, producer.history().size());
        producer.history().forEach(r -> {
            assertEquals("kates-probe-topic", r.topic());
            assertEquals(KafkaProbeChecks.PRODUCE_RECORD_BYTES, r.value().length);
        });
        Properties props = config.get();
        assertEquals("all", props.get(ProducerConfig.ACKS_CONFIG));
        assertEquals("krafter-kafka-bootstrap.kafka.svc:9092", props.get(ProducerConfig.BOOTSTRAP_SERVERS_CONFIG));
        assertTrue(props.get(ProducerConfig.CLIENT_ID_CONFIG).toString().startsWith("kates-probe-"));
        assertTrue((int) props.get(ProducerConfig.DELIVERY_TIMEOUT_MS_CONFIG) <= 30_000);
        assertTrue((int) props.get(ProducerConfig.MAX_BLOCK_MS_CONFIG) <= 30_000);
        verify(securityConfig).apply(props);
        assertTrue(producer.closed());
    }

    /** The old probe wrote to kates-probe-topic, which nothing created, and the brokers create no topic on use. */
    @Test
    void produceToATopicWhoseMetadataCannotBeReadFails() {
        MockProducer<byte[], byte[]> producer = producer(true);
        producer.partitionsForException =
                new TimeoutException("Topic kates-probe-topic not present in metadata after 30000 ms.");
        checks.producerFactory = props -> producer;

        ProbeFailure failure = assertThrows(ProbeFailure.class, () -> checks.run("produce kates-probe-topic", TIMEOUT));

        assertEquals(
                "reading the metadata of topic kates-probe-topic failed: TimeoutException: "
                        + "Topic kates-probe-topic not present in metadata after 30000 ms.",
                failure.getMessage());
        assertTrue(producer.history().isEmpty());
        assertTrue(producer.closed());
    }

    @Test
    void produceWhoseRecordsAreAllRefusedFails() throws Exception {
        MockProducer<byte[], byte[]> producer = producer(false);
        checks.producerFactory = props -> producer;
        Thread refuser = Thread.ofVirtual().start(() -> {
            for (int refused = 0; refused < KafkaProbeChecks.PRODUCE_RECORDS; ) {
                if (producer.errorNext(new NotEnoughReplicasException(
                        "Messages are rejected since there are fewer in-sync replicas than required."))) {
                    refused++;
                } else {
                    Thread.onSpinWait();
                }
            }
        });

        ProbeFailure failure = assertThrows(ProbeFailure.class, () -> checks.run("produce kates-probe-topic", TIMEOUT));
        refuser.join();

        assertEquals(
                "no record sent to kates-probe-topic was acknowledged within 10 s: NotEnoughReplicasException: "
                        + "Messages are rejected since there are fewer in-sync replicas than required.",
                failure.getMessage());
    }

    @Test
    void consumerLagAddsUpTheCommittedLagOfEveryGroup() {
        groups("dns-failure-cg", "kates-webhooks");
        offsets(Map.of(
                "dns-failure-cg",
                Map.of(tp(0), new OffsetAndMetadata(90), tp(1), new OffsetAndMetadata(5)),
                "kates-webhooks",
                Map.of(tp(0), new OffsetAndMetadata(50))));
        ends(Map.of(tp(0), 100L, tp(1), 5L));

        assertEquals("60", checks.run("consumer-lag", TIMEOUT));
    }

    @Test
    @SuppressWarnings("unchecked")
    void consumerLagOfOneGroupReadsOnlyThatGroup() {
        groups("dns-failure-cg", "kates-webhooks");
        offsets(Map.of("kates-webhooks", Map.of(tp(0), new OffsetAndMetadata(50))));
        ends(Map.of(tp(0), 100L));

        assertEquals("50", checks.run("consumer-lag kates-webhooks", TIMEOUT));

        ArgumentCaptor<Map<String, ListConsumerGroupOffsetsSpec>> specs = ArgumentCaptor.forClass(Map.class);
        verify(admin).listConsumerGroupOffsets(specs.capture(), any(ListConsumerGroupOffsetsOptions.class));
        assertEquals(Set.of("kates-webhooks"), specs.getValue().keySet());
    }

    /** A group that does not exist has committed nothing, so its lag used to read 0 and pass. */
    @Test
    void consumerLagOfAGroupThatDoesNotExistFails() {
        groups("kates-webhooks");

        ProbeFailure failure =
                assertThrows(ProbeFailure.class, () -> checks.run("consumer-lag dns-failure-cg", TIMEOUT));

        assertEquals("consumer group dns-failure-cg does not exist", failure.getMessage());
    }

    @Test
    void aCommandThatNamesNoCheckFails() {
        assertEquals(
                "a kafkaProbe has no check 'kafka-topics.sh'; use one of [under-replicated-partitions, "
                        + "unavailable-partitions, produce <topic>, consumer-lag [<group>]]",
                assertThrows(ProbeFailure.class, () -> checks.run("kafka-topics.sh --describe", TIMEOUT))
                        .getMessage());
        assertTrue(assertThrows(ProbeFailure.class, () -> checks.run("  ", TIMEOUT))
                .getMessage()
                .startsWith("a kafkaProbe needs a command naming its check"));
    }

    @Test
    void aCommandWithTheWrongArgumentsFails() {
        assertEquals(
                "kafkaProbe command 'produce' does not match 'produce <topic>'",
                assertThrows(ProbeFailure.class, () -> checks.run("produce", TIMEOUT))
                        .getMessage());
        assertEquals(
                "kafkaProbe command 'unavailable-partitions orders' does not match 'unavailable-partitions'",
                assertThrows(ProbeFailure.class, () -> checks.run("unavailable-partitions orders", TIMEOUT))
                        .getMessage());
        assertEquals(
                "kafkaProbe command 'consumer-lag a b' does not match 'consumer-lag [<group>]'",
                assertThrows(ProbeFailure.class, () -> checks.run("consumer-lag a b", TIMEOUT))
                        .getMessage());
    }

    private static TopicPartitionInfo partition(int id, Node leader, List<Node> replicas, List<Node> isr) {
        return new TopicPartitionInfo(id, leader, replicas, isr);
    }

    private static TopicPartition tp(int partition) {
        return new TopicPartition("kates-events", partition);
    }

    private static MockProducer<byte[], byte[]> producer(boolean autoComplete) {
        return new MockProducer<>(autoComplete, new ByteArraySerializer(), new ByteArraySerializer());
    }

    private void topics(TopicDescription... descriptions) {
        ListTopicsResult listed = mock(ListTopicsResult.class);
        Map<String, TopicDescription> byName = new java.util.HashMap<>();
        for (TopicDescription d : descriptions) {
            byName.put(d.name(), d);
        }
        when(listed.names()).thenReturn(KafkaFuture.completedFuture(byName.keySet()));
        when(admin.listTopics(any(ListTopicsOptions.class))).thenReturn(listed);
        DescribeTopicsResult described = mock(DescribeTopicsResult.class);
        when(described.allTopicNames()).thenReturn(KafkaFuture.completedFuture(byName));
        when(admin.describeTopics(anyCollection(), any(DescribeTopicsOptions.class)))
                .thenReturn(described);
    }

    private void reassignments(Map<TopicPartition, PartitionReassignment> reassignments) {
        ListPartitionReassignmentsResult result = mock(ListPartitionReassignmentsResult.class);
        when(result.reassignments()).thenReturn(KafkaFuture.completedFuture(reassignments));
        when(admin.listPartitionReassignments(any(ListPartitionReassignmentsOptions.class)))
                .thenReturn(result);
    }

    private void groups(String... groupIds) {
        ListConsumerGroupsResult result = mock(ListConsumerGroupsResult.class);
        when(result.all())
                .thenReturn(KafkaFuture.completedFuture(java.util.Arrays.stream(groupIds)
                        .map(g -> new ConsumerGroupListing(g, false))
                        .toList()));
        when(admin.listConsumerGroups(any(ListConsumerGroupsOptions.class))).thenReturn(result);
    }

    private void offsets(Map<String, Map<TopicPartition, OffsetAndMetadata>> committed) {
        ListConsumerGroupOffsetsResult result = mock(ListConsumerGroupOffsetsResult.class);
        when(result.all()).thenReturn(KafkaFuture.completedFuture(committed));
        when(admin.listConsumerGroupOffsets(anyMap(), any(ListConsumerGroupOffsetsOptions.class)))
                .thenReturn(result);
    }

    private void ends(Map<TopicPartition, Long> offsets) {
        Map<TopicPartition, ListOffsetsResultInfo> infos = new java.util.HashMap<>();
        offsets.forEach((tp, offset) -> infos.put(tp, new ListOffsetsResultInfo(offset, -1, Optional.empty())));
        ListOffsetsResult result = mock(ListOffsetsResult.class);
        when(result.all()).thenReturn(KafkaFuture.completedFuture(infos));
        when(admin.listOffsets(anyMap(), any(ListOffsetsOptions.class))).thenReturn(result);
    }
}
