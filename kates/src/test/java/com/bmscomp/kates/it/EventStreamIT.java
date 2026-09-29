package com.bmscomp.kates.it;

import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStreamReader;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;
import jakarta.enterprise.event.Event;
import jakarta.inject.Inject;

import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.common.http.TestHTTPResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.engine.TestLifecycleEvent;

/**
 * {@code GET /api/events/stream} — the server-sent-events feed the UI subscribes
 * to, which had no test at all.
 *
 * <p>It cannot be driven with RestAssured: the endpoint never closes the
 * response, so any client that waits for a complete body blocks forever. This
 * reads the stream incrementally with the JDK HTTP client instead and pushes
 * each SSE line onto a queue, which also makes the "this event must NOT arrive"
 * assertion expressible.
 *
 * <p>Events reach the resource through CDI async observation, so the test fires
 * them the way the orchestrator does rather than reaching into the resource.
 */
@QuarkusTest
@TestProfile(NoSchedulersTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
class EventStreamIT {

    private static final Duration RECEIVE_TIMEOUT = Duration.ofSeconds(10);

    /**
     * How long to wait before concluding a filtered-out event really was
     * dropped. Long enough that a slow-but-working delivery would have arrived.
     */
    private static final Duration SILENCE_WINDOW = Duration.ofSeconds(3);

    @TestHTTPResource("/")
    URI baseUri;

    @Inject
    Event<TestLifecycleEvent> lifecycleEvents;

    @Test
    void subscriberIsGreetedThenReceivesMatchingEvents() throws Exception {
        try (Stream stream = Stream.open(baseUri, "/api/events/stream")) {
            // The handshake event is what tells a browser the subscription is
            // live; without it a client cannot distinguish "connected and idle"
            // from "still connecting".
            String greeting = stream.awaitData(RECEIVE_TIMEOUT);
            assertNotNull(greeting, "the stream must greet a new subscriber");
            assertTrue(greeting.contains("\"message\":\"connected\""), "unexpected greeting: " + greeting);

            String runId = "sse-run-" + System.nanoTime();
            lifecycleEvents.fireAsync(new TestLifecycleEvent(runId, "LOAD", TestLifecycleEvent.EventKind.DONE, "done"));

            String data = stream.awaitData(RECEIVE_TIMEOUT);
            assertNotNull(data, "a fired lifecycle event must reach the subscriber");
            assertTrue(data.contains(runId), "event payload should name the run: " + data);
            assertTrue(data.contains("\"event\":\"DONE\""), "event payload should carry its kind: " + data);
        }
    }

    @Test
    void typeFilterDropsEventsForOtherTestTypes() throws Exception {
        try (Stream stream = Stream.open(baseUri, "/api/events/stream?type=LOAD")) {
            assertNotNull(stream.awaitData(RECEIVE_TIMEOUT), "greeting");

            // Wrong type: must be filtered out server-side.
            lifecycleEvents.fireAsync(
                    new TestLifecycleEvent("stress-run", "STRESS", TestLifecycleEvent.EventKind.DONE, null));
            assertNull(
                    stream.awaitData(SILENCE_WINDOW),
                    "an event for another test type must not reach a filtered subscriber");

            // Right type: proves the silence above was the filter and not a
            // dead stream.
            String matching = "load-run-" + System.nanoTime();
            lifecycleEvents.fireAsync(
                    new TestLifecycleEvent(matching, "LOAD", TestLifecycleEvent.EventKind.RUNNING, null));
            String data = stream.awaitData(RECEIVE_TIMEOUT);
            assertNotNull(data, "a matching event must still be delivered");
            assertTrue(data.contains(matching), "unexpected payload: " + data);
        }
    }

    @Test
    void idFilterNarrowsToASingleRun() throws Exception {
        String wanted = "wanted-" + System.nanoTime();
        try (Stream stream = Stream.open(baseUri, "/api/events/stream?id=" + wanted)) {
            assertNotNull(stream.awaitData(RECEIVE_TIMEOUT), "greeting");

            lifecycleEvents.fireAsync(
                    new TestLifecycleEvent("some-other-run", "LOAD", TestLifecycleEvent.EventKind.DONE, null));
            assertNull(stream.awaitData(SILENCE_WINDOW), "another run's events must not leak into a filtered stream");

            lifecycleEvents.fireAsync(new TestLifecycleEvent(wanted, "LOAD", TestLifecycleEvent.EventKind.DONE, null));
            String data = stream.awaitData(RECEIVE_TIMEOUT);
            assertNotNull(data, "the subscribed run's events must arrive");
            assertTrue(data.contains(wanted));
        }
    }

    /**
     * A minimal SSE reader: consumes the response body on a daemon thread and
     * publishes the {@code data:} payloads onto a queue so tests can wait for
     * one — or assert that none arrives.
     */
    private static final class Stream implements AutoCloseable {

        private final BlockingQueue<String> data = new LinkedBlockingQueue<>();
        private final HttpClient client;
        private final HttpResponse<java.io.InputStream> response;
        private final Thread reader;

        private Stream(HttpClient client, HttpResponse<java.io.InputStream> response) {
            this.client = client;
            this.response = response;
            this.reader = new Thread(this::pump, "sse-it-reader");
            this.reader.setDaemon(true);
            this.reader.start();
        }

        static Stream open(URI baseUri, String path) throws IOException, InterruptedException {
            HttpClient client = HttpClient.newBuilder()
                    .connectTimeout(Duration.ofSeconds(5))
                    .build();
            HttpRequest request = HttpRequest.newBuilder(baseUri.resolve(path))
                    .header("Accept", "text/event-stream")
                    .GET()
                    .build();
            HttpResponse<java.io.InputStream> response =
                    client.send(request, HttpResponse.BodyHandlers.ofInputStream());
            if (response.statusCode() != 200) {
                throw new IllegalStateException("SSE subscribe failed with " + response.statusCode());
            }
            return new Stream(client, response);
        }

        private void pump() {
            try (BufferedReader in =
                    new BufferedReader(new InputStreamReader(response.body(), StandardCharsets.UTF_8))) {
                String line;
                while ((line = in.readLine()) != null) {
                    if (line.startsWith("data:")) {
                        data.offer(line.substring("data:".length()).trim());
                    }
                }
            } catch (IOException e) {
                // Expected on close(): the body is interrupted mid-read.
            }
        }

        /** The next {@code data:} payload, or null if none arrives in time. */
        String awaitData(Duration timeout) throws InterruptedException {
            return data.poll(timeout.toMillis(), TimeUnit.MILLISECONDS);
        }

        @Override
        public void close() {
            reader.interrupt();
            try {
                response.body().close();
            } catch (IOException ignored) {
                // Nothing useful to do while tearing the subscription down.
            }
            client.close();
        }
    }
}
