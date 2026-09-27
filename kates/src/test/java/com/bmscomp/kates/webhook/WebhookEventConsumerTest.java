package com.bmscomp.kates.webhook;

import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.UUID;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import io.quarkus.test.junit.QuarkusTest;
import io.smallrye.reactive.messaging.memory.InMemoryConnector;
import io.smallrye.reactive.messaging.memory.InMemorySource;
import org.eclipse.microprofile.reactive.messaging.spi.Connector;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.events.TestEvent;

/**
 * Lifecycle events delivered through the {@code test-events-in} channel, the way
 * the Kafka consumer delivers them, reach a registered webhook.
 *
 * <p>{@code WebhookDeliveryIT} calls {@link WebhookService#onTestEvent} on the
 * test thread, where Quarkus keeps a request context active. The channel calls
 * it on a messaging worker thread, which has none, and that is the thread the
 * webhook path runs on in production. There the registration lookup threw
 * {@code ContextNotActiveException} for every DONE or FAILED event; on Kafka the
 * nack that follows stops the channel. The in-memory connector dispatches the
 * same way, so this needs no broker.
 */
@QuarkusTest
class WebhookEventConsumerTest {

    private static final Duration DELIVERY_TIMEOUT = Duration.ofSeconds(15);

    /** Long enough that a delivery for a non-terminal event would have landed. */
    private static final Duration SILENCE_WINDOW = Duration.ofSeconds(3);

    @Inject
    @Connector("smallrye-in-memory")
    InMemoryConnector connector;

    @Inject
    WebhookService webhookService;

    @Inject
    EntityManager em;

    private HttpServer server;
    private final BlockingQueue<String> received = new LinkedBlockingQueue<>();
    private String hookName;

    @BeforeEach
    void startReceiver() throws IOException {
        received.clear();
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext("/hook", this::handle);
        server.start();
        hookName = "consumer-hook-" + UUID.randomUUID();
        webhookService.register(new WebhookService.WebhookRegistration(
                hookName, "http://127.0.0.1:" + server.getAddress().getPort() + "/hook", "DONE"));
    }

    @AfterEach
    void stopReceiver() {
        webhookService.unregister(hookName);
        if (server != null) {
            server.stop(0);
        }
    }

    private void handle(HttpExchange exchange) throws IOException {
        received.offer(new String(exchange.getRequestBody().readAllBytes(), StandardCharsets.UTF_8));
        exchange.sendResponseHeaders(200, -1);
        exchange.close();
    }

    @Test
    void aTerminalEventOnTheChannelCallsTheWebhook() throws Exception {
        InMemorySource<TestEvent> events = connector.source("test-events-in");
        String runId = "channel-run-" + UUID.randomUUID();

        events.send(new TestEvent(runId, "LOAD", TestResult.TaskStatus.RUNNING, "", System.currentTimeMillis()));
        events.send(new TestEvent(runId, "LOAD", TestResult.TaskStatus.DONE, "", System.currentTimeMillis()));

        String delivery = received.poll(DELIVERY_TIMEOUT.toMillis(), TimeUnit.MILLISECONDS);
        assertNotNull(delivery, "the DONE event read from the channel must call the webhook");
        assertTrue(delivery.contains(runId), "payload must name the run: " + delivery);
        assertTrue(delivery.contains("\"status\":\"DONE\""), "payload was " + delivery);
        assertNull(
                received.poll(SILENCE_WINDOW.toMillis(), TimeUnit.MILLISECONDS), "the RUNNING event must not notify");
    }

    /**
     * The consumer group re-reads the topic from its start when it has no
     * committed offset, and the topic can hold events older than
     * processed_events remembers. Such an event may have notified already, so it
     * must not notify again.
     */
    @Test
    void anEventOlderThanTheLedgerIsSkipped() throws Exception {
        InMemorySource<TestEvent> events = connector.source("test-events-in");
        String staleRun = "stale-run-" + UUID.randomUUID();
        String freshRun = "fresh-run-" + UUID.randomUUID();
        long eightDaysAgo = Instant.now().minus(8, ChronoUnit.DAYS).toEpochMilli();

        events.send(new TestEvent(staleRun, "LOAD", TestResult.TaskStatus.DONE, "", eightDaysAgo));
        events.send(new TestEvent(freshRun, "LOAD", TestResult.TaskStatus.DONE, "", System.currentTimeMillis()));

        String delivery = received.poll(DELIVERY_TIMEOUT.toMillis(), TimeUnit.MILLISECONDS);
        assertNotNull(delivery, "the fresh DONE event, sent after the stale one, must call the webhook");
        assertTrue(delivery.contains(freshRun), "the stale event must not notify, but this did: " + delivery);
        assertNull(received.poll(SILENCE_WINDOW.toMillis(), TimeUnit.MILLISECONDS), "only the fresh event notifies");
        assertNull(
                em.find(ProcessedEventEntity.class, staleRun + ":DONE"),
                "a skipped event leaves no ledger row for the next sweep to prune");
    }
}
