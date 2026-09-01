package com.bmscomp.kates.it;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.hasItem;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.Map;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.restassured.http.ContentType;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.events.TestEvent;
import com.bmscomp.kates.webhook.WebhookService;

/**
 * Webhook delivery end to end: registration through the API, an event through
 * the service, a real HTTP POST out, and the failure paths that follow.
 *
 * <p>{@code WebhookResourceTest} mocks {@code WebhookService} entirely, so the
 * delivery logic — payload shape, headers, retry, idempotency and the dead
 * letter row — has never executed under test. The receiving end here is the
 * JDK's own {@code HttpServer} rather than a mocking library, so no new
 * dependency is needed to observe what was actually sent.
 */
@QuarkusTest
@TestProfile(NoSchedulersTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
class WebhookDeliveryIT {

    private static final Duration DELIVERY_TIMEOUT = Duration.ofSeconds(15);

    /** Long enough that a duplicate delivery would have landed if it were coming. */
    private static final Duration SILENCE_WINDOW = Duration.ofSeconds(3);

    @Inject
    WebhookService webhookService;

    @Inject
    EntityManager em;

    private HttpServer server;
    private final BlockingQueue<Received> received = new LinkedBlockingQueue<>();
    private final AtomicInteger responseStatus = new AtomicInteger(200);

    @BeforeEach
    void startReceiver() throws IOException {
        ItSupport.truncate(em, "webhook_registrations", "webhook_dlq", "processed_events");
        received.clear();
        responseStatus.set(200);

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
        byte[] body = exchange.getRequestBody().readAllBytes();
        received.offer(new Received(
                new String(body, StandardCharsets.UTF_8),
                exchange.getRequestHeaders().getFirst("X-Kates-Event"),
                exchange.getRequestHeaders().getFirst("X-Kates-Attempt"),
                exchange.getRequestHeaders().getFirst("Content-Type")));
        exchange.sendResponseHeaders(responseStatus.get(), -1);
        exchange.close();
    }

    private String hookUrl() {
        return "http://127.0.0.1:" + server.getAddress().getPort() + "/hook";
    }

    @Test
    void registeredWebhookReceivesTheCompletionPayload() throws Exception {
        given().contentType(ContentType.JSON)
                .body(Map.of("name", "it-hook", "url", hookUrl(), "events", "DONE"))
                .when()
                .post("/api/webhooks")
                .then()
                .statusCode(201);

        given().when().get("/api/webhooks").then().statusCode(200).body("name", hasItem("it-hook"));

        String runId = "hook-run-" + System.nanoTime();
        webhookService.onTestEvent(
                new TestEvent(runId, "LOAD", TestResult.TaskStatus.DONE, "finished", System.currentTimeMillis()));

        Received delivery = received.poll(DELIVERY_TIMEOUT.toMillis(), TimeUnit.MILLISECONDS);
        assertNotNull(delivery, "a registered webhook must actually be called");

        // Delivery runs on a virtual thread and is fire-and-forget, so the only
        // evidence of what was sent is what the receiver saw.
        assertTrue(delivery.body().contains("\"event\":\"test.completed\""), "payload was " + delivery.body());
        assertTrue(delivery.body().contains(runId), "payload must name the run: " + delivery.body());
        assertTrue(delivery.body().contains("\"status\":\"DONE\""), "payload was " + delivery.body());
        assertEquals("test.completed", delivery.eventHeader(), "receivers route on X-Kates-Event");
        assertEquals("1", delivery.attemptHeader(), "a first delivery is attempt 1");
        assertTrue(delivery.contentType().contains("application/json"), "content type was " + delivery.contentType());
    }

    @Test
    void theSameEventIsNeverDeliveredTwice() throws Exception {
        webhookService.register(new WebhookService.WebhookRegistration("idem-hook", hookUrl(), "DONE"));

        String runId = "idem-run-" + System.nanoTime();
        TestEvent event =
                new TestEvent(runId, "LOAD", TestResult.TaskStatus.DONE, "finished", System.currentTimeMillis());

        webhookService.onTestEvent(event);
        assertNotNull(received.poll(DELIVERY_TIMEOUT.toMillis(), TimeUnit.MILLISECONDS), "first delivery");

        // Redelivery of the same test/status pair is what the processed_events
        // table exists to prevent; the guard only works against a real unique
        // constraint, so it cannot be observed anywhere but here.
        webhookService.onTestEvent(event);
        assertNull(
                received.poll(SILENCE_WINDOW.toMillis(), TimeUnit.MILLISECONDS),
                "a replayed event must be suppressed by the idempotency key");
    }

    @Test
    void inFlightStatusesAreNotDelivered() throws Exception {
        webhookService.register(new WebhookService.WebhookRegistration("running-hook", hookUrl(), "DONE"));

        webhookService.onTestEvent(new TestEvent(
                "running-run-" + System.nanoTime(),
                "LOAD",
                TestResult.TaskStatus.RUNNING,
                "started",
                System.currentTimeMillis()));

        assertNull(
                received.poll(SILENCE_WINDOW.toMillis(), TimeUnit.MILLISECONDS),
                "only terminal statuses notify webhooks");
    }

    @Test
    void aFailingEndpointIsRetriedAndThenDeadLettered() throws Exception {
        responseStatus.set(500);
        webhookService.register(new WebhookService.WebhookRegistration("dlq-hook", hookUrl(), "DONE"));

        String runId = "dlq-run-" + System.nanoTime();
        webhookService.onTestEvent(
                new TestEvent(runId, "LOAD", TestResult.TaskStatus.DONE, "finished", System.currentTimeMillis()));

        // Three attempts with 1s and 2s backoff, then the payload is parked.
        assertNotNull(received.poll(DELIVERY_TIMEOUT.toMillis(), TimeUnit.MILLISECONDS), "attempt 1");
        assertNotNull(received.poll(DELIVERY_TIMEOUT.toMillis(), TimeUnit.MILLISECONDS), "attempt 2");
        assertNotNull(received.poll(DELIVERY_TIMEOUT.toMillis(), TimeUnit.MILLISECONDS), "attempt 3");

        assertTrue(
                ItSupport.waitUntil(Duration.ofSeconds(20), () -> dlqRowsFor("dlq-hook") == 1),
                "an exhausted webhook must leave exactly one dead-letter row, found " + dlqRowsFor("dlq-hook"));
    }

    @Test
    void registrationRejectsUrlsThePolicyForbids() {
        given().contentType(ContentType.JSON)
                .body(Map.of("name", "bad-scheme", "url", "ftp://example.com/hook", "events", "DONE"))
                .when()
                .post("/api/webhooks")
                .then()
                .statusCode(400);

        given().contentType(ContentType.JSON)
                .body(Map.of("url", hookUrl(), "events", "DONE"))
                .when()
                .post("/api/webhooks")
                .then()
                .statusCode(400);

        given().contentType(ContentType.JSON)
                .body(Map.of("name", "no-url", "events", "DONE"))
                .when()
                .post("/api/webhooks")
                .then()
                .statusCode(400);
    }

    private long dlqRowsFor(String webhookName) {
        return ((Number) em.createNativeQuery("SELECT COUNT(*) FROM webhook_dlq WHERE webhook_name = :name")
                        .setParameter("name", webhookName)
                        .getSingleResult())
                .longValue();
    }

    private record Received(String body, String eventHeader, String attemptHeader, String contentType) {}
}
