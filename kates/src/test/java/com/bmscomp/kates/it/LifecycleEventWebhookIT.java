package com.bmscomp.kates.it;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.List;
import java.util.Map;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicReference;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.common.ResourceArg;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.Config;
import org.apache.kafka.clients.admin.OffsetSpec;
import org.apache.kafka.clients.admin.TopicDescription;
import org.apache.kafka.clients.consumer.OffsetAndMetadata;
import org.apache.kafka.common.Node;
import org.apache.kafka.common.TopicPartition;
import org.apache.kafka.common.config.ConfigResource;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.KafkaAdminService;
import com.bmscomp.kates.service.OutboxPoller;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.webhook.WebhookService;

/**
 * A run's lifecycle events travel the whole path to a webhook: the outbox row
 * written with the run, the poller publishing it to Kafka, the Kates API's own
 * consumer reading it back, and an HTTP POST to the registered URL.
 *
 * <p>No test covered the middle of that path. Every other test wires the two
 * event-bus channels to the in-memory connector, which never hands an event from
 * one to the other, and {@code WebhookDeliveryIT} calls the consumer method
 * directly. So nothing noticed that on {@code krafter} the topic between them
 * did not exist: the broker refuses to auto-create topics, nothing else created
 * {@code kates-test-events}, every event was dead-lettered and no webhook fired.
 *
 * <p>The broker here refuses to auto-create topics too, so this passes only
 * because the Kates API creates the topic itself.
 */
@QuarkusTest
@TestProfile(EventBusTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
@QuarkusTestResource(
        value = KafkaTestResource.class,
        restrictToAnnotatedClass = true,
        initArgs = @ResourceArg(name = KafkaTestResource.AUTO_CREATE_TOPICS, value = "false"))
class LifecycleEventWebhookIT {

    /** A cold consumer has to find the new topic and join its group first. */
    private static final Duration DELIVERY_TIMEOUT = Duration.ofSeconds(60);

    /** Long enough that a delivery for a non-terminal event would have landed. */
    private static final Duration SILENCE_WINDOW = Duration.ofSeconds(5);

    private static final long ADMIN_TIMEOUT_SECONDS = 10;

    @Inject
    TestRunRepository repository;

    @Inject
    OutboxPoller poller;

    @Inject
    WebhookService webhookService;

    @Inject
    KafkaAdminService adminService;

    @Inject
    EntityManager em;

    private HttpServer server;
    private final BlockingQueue<String> received = new LinkedBlockingQueue<>();

    @BeforeEach
    void startReceiver() throws IOException {
        ItSupport.truncate(
                em, "webhook_registrations", "webhook_dlq", "processed_events", "outbox_events", "outbox_dead_letters");
        received.clear();

        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext("/hook", this::handle);
        server.start();
    }

    @AfterEach
    void stopReceiver() {
        if (server != null) {
            server.stop(0);
        }
    }

    private void handle(HttpExchange exchange) throws IOException {
        received.offer(new String(exchange.getRequestBody().readAllBytes(), StandardCharsets.UTF_8));
        exchange.sendResponseHeaders(200, -1);
        exchange.close();
    }

    private String hookUrl() {
        return "http://127.0.0.1:" + server.getAddress().getPort() + "/hook";
    }

    @Test
    void theKatesApiCreatesItsEventTopicOnABrokerThatWillNot() throws Exception {
        AdminClient admin = adminService.sharedAdminClient();
        Node broker = admin.describeCluster()
                .nodes()
                .get(ADMIN_TIMEOUT_SECONDS, TimeUnit.SECONDS)
                .iterator()
                .next();
        assertEquals(
                "false",
                configOf(admin, new ConfigResource(ConfigResource.Type.BROKER, broker.idString()))
                        .get("auto.create.topics.enable")
                        .value(),
                "the premise: this broker, like krafter, creates no topic on first use");

        TopicDescription topic = awaitTopic();

        assertEquals(1, topic.partitions().size(), "a run writes a handful of events");
        assertEquals(
                1,
                topic.partitions().get(0).replicas().size(),
                "replication factor 3 is capped at the one broker there is");
        Config config = configOf(admin, new ConfigResource(ConfigResource.Type.TOPIC, EventBusTestProfile.TOPIC));
        assertEquals("delete", config.get("cleanup.policy").value());
        assertEquals("604800000", config.get("retention.ms").value());
    }

    @Test
    void aFinishedRunReachesARegisteredWebhookThroughKafka() throws Exception {
        awaitTopic();
        webhookService.register(new WebhookService.WebhookRegistration("bus-hook", hookUrl(), "DONE"));

        TestSpec spec = new TestSpec();
        spec.setTopic("event-bus-it");
        TestRun run = new TestRun(TestType.LOAD, spec).withStatus(TestResult.TaskStatus.PENDING);
        repository.save(run);
        repository.save(run.withStatus(TestResult.TaskStatus.RUNNING));
        repository.save(run.withStatus(TestResult.TaskStatus.DONE));
        em.clear();
        assertEquals(3, outboxRowsFor(run.getId()), "one lifecycle event per status change");

        // The scheduled poller is off in this profile. Poll until every row is
        // acknowledged and deleted: a send that fails leaves its row for the
        // next poll, exactly as in production.
        assertTrue(
                ItSupport.waitUntil(DELIVERY_TIMEOUT, () -> {
                    poller.processOutbox();
                    em.clear();
                    return outboxRowsFor(run.getId()) == 0;
                }),
                "the broker acknowledged all three events");
        assertEquals(0, deadLettersFor(run.getId()), "no event was given up on");

        String delivery = received.poll(DELIVERY_TIMEOUT.toMillis(), TimeUnit.MILLISECONDS);
        assertNotNull(delivery, "the consumer read the DONE event back and called the webhook");
        assertTrue(delivery.contains("\"event\":\"test.completed\""), "payload was " + delivery);
        assertTrue(delivery.contains(run.getId()), "payload must name the run: " + delivery);
        assertTrue(delivery.contains("\"status\":\"DONE\""), "payload was " + delivery);

        assertNull(
                received.poll(SILENCE_WINDOW.toMillis(), TimeUnit.MILLISECONDS),
                "PENDING and RUNNING reach the consumer too, but only a terminal status notifies");

        // The consumer commits what it has read in a group every replica shares,
        // so a pod that replaces this one starts after these events, and reads
        // anything published while no pod was reading.
        TopicPartition partition = new TopicPartition(EventBusTestProfile.TOPIC, 0);
        AdminClient admin = adminService.sharedAdminClient();
        long end = admin.listOffsets(Map.of(partition, OffsetSpec.latest()))
                .partitionResult(partition)
                .get(ADMIN_TIMEOUT_SECONDS, TimeUnit.SECONDS)
                .offset();
        assertTrue(end >= 3, "the three events are on the topic's one partition");
        assertTrue(
                ItSupport.waitUntil(DELIVERY_TIMEOUT, () -> committedOffset(admin, partition) == end),
                EventBusTestProfile.GROUP + " committed every event it read");
    }

    /** Waits for the provisioner's startup check; nothing in the test creates the topic. */
    private TopicDescription awaitTopic() {
        AtomicReference<TopicDescription> found = new AtomicReference<>();
        boolean exists = ItSupport.waitUntil(DELIVERY_TIMEOUT, () -> {
            try {
                found.set(adminService
                        .sharedAdminClient()
                        .describeTopics(List.of(EventBusTestProfile.TOPIC))
                        .topicNameValues()
                        .get(EventBusTestProfile.TOPIC)
                        .get(ADMIN_TIMEOUT_SECONDS, TimeUnit.SECONDS));
                return true;
            } catch (Exception e) {
                return false;
            }
        });
        assertTrue(exists, "the Kates API creates " + EventBusTestProfile.TOPIC + " without being asked");
        return found.get();
    }

    /** The webhook consumer group's committed offset on {@code partition}, or -1 if it has none. */
    private static long committedOffset(AdminClient admin, TopicPartition partition) {
        try {
            OffsetAndMetadata committed = admin.listConsumerGroupOffsets(EventBusTestProfile.GROUP)
                    .partitionsToOffsetAndMetadata()
                    .get(ADMIN_TIMEOUT_SECONDS, TimeUnit.SECONDS)
                    .get(partition);
            return committed == null ? -1 : committed.offset();
        } catch (Exception e) {
            return -1;
        }
    }

    private static Config configOf(AdminClient admin, ConfigResource resource) throws Exception {
        return admin.describeConfigs(List.of(resource))
                .all()
                .get(ADMIN_TIMEOUT_SECONDS, TimeUnit.SECONDS)
                .get(resource);
    }

    private long outboxRowsFor(String runId) {
        return ((Number) em.createNativeQuery("SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = :id")
                        .setParameter("id", runId)
                        .getSingleResult())
                .longValue();
    }

    private long deadLettersFor(String runId) {
        return ((Number) em.createNativeQuery("SELECT COUNT(*) FROM outbox_dead_letters WHERE aggregate_id = :id")
                        .setParameter("id", runId)
                        .getSingleResult())
                .longValue();
    }
}
