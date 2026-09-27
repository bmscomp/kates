package com.bmscomp.kates.it;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.time.Duration;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.TimeUnit;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.common.ResourceArg;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import org.apache.kafka.clients.CommonClientConfigs;
import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.ConsumerGroupDescription;
import org.apache.kafka.clients.admin.MemberDescription;
import org.apache.kafka.clients.admin.NewTopic;
import org.apache.kafka.clients.consumer.OffsetAndMetadata;
import org.apache.kafka.clients.producer.KafkaProducer;
import org.apache.kafka.clients.producer.ProducerRecord;
import org.apache.kafka.common.TopicPartition;
import org.apache.kafka.common.serialization.StringSerializer;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.events.TestEvent;
import com.bmscomp.kates.service.KafkaAdminService;

/**
 * The webhook consumer reads in one consumer group that outlives any pod, so an
 * event published while no Kates API was reading still reaches the webhooks.
 *
 * <p>The channel used to set no {@code group.id}, so the connector made up a new
 * group at every start, and that group began at the end of the topic. An event
 * published while no pod was reading, or before a new pod's group got its first
 * assignment, was never read.
 *
 * <p>{@link StoppedPodKafka} leaves on {@code kates-test-events}, before this
 * class's Kates API starts, what a Kates API that stopped would have left:
 * <ul>
 *   <li>Partition 0 holds two events, and {@code kates-webhooks} has committed
 *       offset 1 there, as if the stopped pod had read the first. The group must
 *       resume at the second event and not read the first again. A group of the
 *       pod's own would read both from the earliest offset, or neither from the
 *       latest.
 *   <li>Partition 1 has no committed offset, the case a brand-new group is in,
 *       so {@code auto.offset.reset} decides where the group starts. It must
 *       start at the beginning and read the second event there. The first is
 *       older than {@code processed_events} remembers, so the consumer must skip
 *       it rather than risk notifying twice.
 * </ul>
 * Production creates the topic with one partition. The second one lets a single
 * start of the Kates API show both the resume and the reset.
 *
 * <p>The consumer records each event it handles in {@code processed_events},
 * which is what the tests read, so no webhook has to be registered before the
 * Kates API starts. {@link LifecycleEventWebhookIT} follows an event on to the
 * webhook.
 */
@QuarkusTest
@TestProfile(EventBusTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
@QuarkusTestResource(
        value = WebhookConsumerGroupIT.StoppedPodKafka.class,
        restrictToAnnotatedClass = true,
        initArgs = @ResourceArg(name = KafkaTestResource.AUTO_CREATE_TOPICS, value = "false"))
class WebhookConsumerGroupIT {

    private static final String READ_BEFORE_THE_STOP = "read-before-the-stop";
    private static final String PUBLISHED_WHILE_DOWN = "published-while-down";
    private static final String OLDER_THAN_THE_LEDGER = "older-than-the-ledger";
    private static final String ON_A_PARTITION_WITH_NO_OFFSET = "on-a-partition-with-no-offset";

    private static final TopicPartition RESUMED = new TopicPartition(EventBusTestProfile.TOPIC, 0);
    private static final TopicPartition RESET = new TopicPartition(EventBusTestProfile.TOPIC, 1);

    /** A cold consumer has to join its group and be assigned the partitions first. */
    private static final Duration DELIVERY_TIMEOUT = Duration.ofSeconds(60);

    private static final long ADMIN_TIMEOUT_SECONDS = 10;

    @Inject
    KafkaAdminService adminService;

    @Inject
    EntityManager em;

    // No truncating processed_events before each test, unlike the other ITs:
    // the consumer can read the events left on the topic before the first test
    // starts, and those rows are what the tests look for.

    @Test
    void theGroupResumesAfterItsCommittedOffsetAndStartsAPartitionWithoutOneAtTheBeginning() {
        assertTrue(
                ItSupport.waitUntil(
                        DELIVERY_TIMEOUT,
                        () -> handled(PUBLISHED_WHILE_DOWN) && handled(ON_A_PARTITION_WITH_NO_OFFSET)),
                "the consumer read both events published while no Kates API was reading");

        // Each partition is read in order, so the events before these two have
        // been read or passed over by now.
        assertFalse(
                handled(READ_BEFORE_THE_STOP),
                "the group resumed after the offset the stopped pod committed, instead of starting again");
        assertFalse(
                handled(OLDER_THAN_THE_LEDGER),
                "an event older than processed_events remembers is skipped, not delivered a second time");
    }

    @Test
    void theKatesApiIsTheGroupsOnlyMemberAndCommitsWhatItReads() throws Exception {
        AdminClient admin = adminService.sharedAdminClient();

        assertTrue(
                ItSupport.waitUntil(
                        DELIVERY_TIMEOUT, () -> Map.of(RESUMED, 2L, RESET, 2L).equals(committedOffsets(admin))),
                () -> "the group committed past every event, skipped ones included: " + committedOffsets(admin));

        ConsumerGroupDescription group = admin.describeConsumerGroups(List.of(EventBusTestProfile.GROUP))
                .describedGroups()
                .get(EventBusTestProfile.GROUP)
                .get(ADMIN_TIMEOUT_SECONDS, TimeUnit.SECONDS);
        assertEquals(1, group.members().size(), "one Kates API, so one member: " + group);
        MemberDescription member = group.members().iterator().next();
        assertEquals(Set.of(RESUMED, RESET), member.assignment().topicPartitions());
    }

    private boolean handled(String runId) {
        em.clear();
        return ((Number) em.createNativeQuery("SELECT COUNT(*) FROM processed_events WHERE idempotency_key = :key")
                                .setParameter("key", runId + ":" + TestResult.TaskStatus.DONE.name())
                                .getSingleResult())
                        .longValue()
                > 0;
    }

    private static Map<TopicPartition, Long> committedOffsets(AdminClient admin) {
        try {
            Map<TopicPartition, Long> offsets = new HashMap<>();
            admin.listConsumerGroupOffsets(EventBusTestProfile.GROUP)
                    .partitionsToOffsetAndMetadata()
                    .get(ADMIN_TIMEOUT_SECONDS, TimeUnit.SECONDS)
                    .forEach((partition, committed) -> {
                        if (committed != null) {
                            offsets.put(partition, committed.offset());
                        }
                    });
            return offsets;
        } catch (Exception e) {
            return Map.of();
        }
    }

    /**
     * {@link KafkaTestResource}, plus what a Kates API that stopped would have left
     * on the event topic, written before this class's Kates API starts.
     */
    public static class StoppedPodKafka extends KafkaTestResource {

        private static final ObjectMapper MAPPER = new ObjectMapper();

        @Override
        public Map<String, String> start() {
            Map<String, String> config = super.start();
            try {
                leaveEventsBehind(config.get("kates.kafka.bootstrap-servers"));
            } catch (Exception e) {
                throw new IllegalStateException("Could not prepare " + EventBusTestProfile.TOPIC, e);
            }
            return config;
        }

        private static void leaveEventsBehind(String bootstrapServers) throws Exception {
            Map<String, Object> client = Map.of(CommonClientConfigs.BOOTSTRAP_SERVERS_CONFIG, bootstrapServers);
            try (AdminClient admin = AdminClient.create(client)) {
                admin.createTopics(List.of(new NewTopic(EventBusTestProfile.TOPIC, 2, (short) 1)))
                        .all()
                        .get(ADMIN_TIMEOUT_SECONDS, TimeUnit.SECONDS);

                long now = System.currentTimeMillis();
                long eightDaysAgo = Instant.now().minus(8, ChronoUnit.DAYS).toEpochMilli();
                try (KafkaProducer<String, String> producer =
                        new KafkaProducer<>(client, new StringSerializer(), new StringSerializer())) {
                    send(producer, RESUMED, READ_BEFORE_THE_STOP, now);
                    send(producer, RESUMED, PUBLISHED_WHILE_DOWN, now);
                    send(producer, RESET, OLDER_THAN_THE_LEDGER, eightDaysAgo);
                    send(producer, RESET, ON_A_PARTITION_WITH_NO_OFFSET, now);
                }

                admin.alterConsumerGroupOffsets(EventBusTestProfile.GROUP, Map.of(RESUMED, new OffsetAndMetadata(1)))
                        .all()
                        .get(ADMIN_TIMEOUT_SECONDS, TimeUnit.SECONDS);
            }
        }

        /** Writes a DONE event as the outbox does: JSON, no key. */
        private static void send(
                KafkaProducer<String, String> producer, TopicPartition partition, String runId, long timestamp)
                throws Exception {
            String event =
                    MAPPER.writeValueAsString(new TestEvent(runId, "LOAD", TestResult.TaskStatus.DONE, "", timestamp));
            producer.send(new ProducerRecord<>(partition.topic(), partition.partition(), null, event))
                    .get(ADMIN_TIMEOUT_SECONDS, TimeUnit.SECONDS);
        }
    }
}
