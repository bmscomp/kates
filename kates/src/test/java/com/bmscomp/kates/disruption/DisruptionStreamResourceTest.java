package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;
import java.util.function.IntSupplier;
import jakarta.inject.Inject;

import io.quarkus.test.common.http.TestHTTPResource;
import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.Test;

/**
 * GET /api/disruptions/{id}/stream sends its run's events, and its listener on
 * the event bus goes when the stream does. Every connection used to leave one
 * behind for good, called on every event of every run after it.
 */
@QuarkusTest
class DisruptionStreamResourceTest {

    @TestHTTPResource("/")
    URI baseUri;

    @Inject
    DisruptionEventBus eventBus;

    @Test
    void aSubscriberGetsItsRunsStepEventsAndItsListenerGoesWhenTheClientHangsUp() throws Exception {
        int before = eventBus.subscriberCount();
        String id = "d-" + System.nanoTime();
        try (HttpClient client = HttpClient.newHttpClient()) {
            HttpResponse<InputStream> response = open(client, id);
            BlockingQueue<String> lines = pump(response.body());
            assertNotNull(await(lines, "event:CONNECTED"));
            assertEquals(before + 1, eventBus.subscriberCount());

            eventBus.emit("other-run", DisruptionEventBus.EventType.STEP_STARTED, "elsewhere", "not this one");
            eventBus.emit(id, DisruptionEventBus.EventType.STEP_STARTED, "kill", "Step started: kill");
            assertNotNull(await(lines, "event:STEP_STARTED"));
            String data = await(lines, "data:");
            assertTrue(data.contains("\"stepName\":\"kill\""), data);

            response.body().close();
        }
        awaitCount(before, eventBus::subscriberCount);
    }

    @Test
    void theListenerGoesWhenTheRunEnds() throws Exception {
        int before = eventBus.subscriberCount();
        String id = "d-" + System.nanoTime();
        try (HttpClient client = HttpClient.newHttpClient()) {
            HttpResponse<InputStream> response = open(client, id);
            BlockingQueue<String> lines = pump(response.body());
            assertNotNull(await(lines, "event:CONNECTED"));

            eventBus.emit(id, DisruptionEventBus.EventType.COMPLETED, null, "Plan completed: COMPLETED");
            assertNotNull(await(lines, "event:COMPLETED"));
            response.body().close();
        }
        awaitCount(before, eventBus::subscriberCount);
    }

    private HttpResponse<InputStream> open(HttpClient client, String id) throws Exception {
        HttpRequest request = HttpRequest.newBuilder(baseUri.resolve("/api/disruptions/" + id + "/stream"))
                .header("Accept", "text/event-stream")
                .build();
        HttpResponse<InputStream> response = client.send(request, HttpResponse.BodyHandlers.ofInputStream());
        assertEquals(200, response.statusCode());
        return response;
    }

    private static BlockingQueue<String> pump(InputStream body) {
        BlockingQueue<String> lines = new LinkedBlockingQueue<>();
        Thread reader = new Thread(() -> {
            try (BufferedReader in = new BufferedReader(new InputStreamReader(body, StandardCharsets.UTF_8))) {
                String line;
                while ((line = in.readLine()) != null) {
                    lines.offer(line.replace("event: ", "event:").replace("data: ", "data:"));
                }
            } catch (IOException e) {
                // the test closed the body
            }
        });
        reader.setDaemon(true);
        reader.start();
        return lines;
    }

    /** The next line starting with prefix, skipping others; null after 10 s. */
    private static String await(BlockingQueue<String> lines, String prefix) throws InterruptedException {
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(10);
        while (System.nanoTime() < deadline) {
            String line = lines.poll(deadline - System.nanoTime(), TimeUnit.NANOSECONDS);
            if (line != null && line.startsWith(prefix)) {
                return line;
            }
        }
        return null;
    }

    private static void awaitCount(int expected, IntSupplier count) throws InterruptedException {
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(10);
        while (count.getAsInt() != expected && System.nanoTime() < deadline) {
            Thread.sleep(50);
        }
        assertEquals(expected, count.getAsInt(), "listeners left after the client hung up");
    }
}
