package com.bmscomp.kates.trogdor;

import static org.junit.jupiter.api.Assertions.*;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CopyOnWriteArrayList;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.sun.net.httpserver.HttpServer;
import io.quarkus.rest.client.reactive.QuarkusRestClientBuilder;
import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult.TaskStatus;
import com.bmscomp.kates.engine.BenchmarkHandle;
import com.bmscomp.kates.engine.BenchmarkStatus;
import com.bmscomp.kates.engine.BenchmarkTask;
import com.bmscomp.kates.engine.TrogdorBackend;

/**
 * The requests the REST client makes, as a coordinator receives them. The
 * paths are CoordinatorRestResource's in apache/kafka's trogdor module; read,
 * stop and destroy used paths it does not serve, so each was a 404.
 *
 * <p>A {@code @QuarkusTest} because the client is generated at build time;
 * the coordinator is a JDK HTTP server that records each request.
 */
@QuarkusTest
class TrogdorClientTest {

    private record Request(String method, String path, String query, String body) {}

    private static final ObjectMapper JSON = new ObjectMapper();

    private final List<Request> requests = new CopyOnWriteArrayList<>();
    private HttpServer coordinator;
    private TrogdorClient client;

    @BeforeEach
    void startCoordinator() throws IOException {
        coordinator = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        coordinator.createContext("/", exchange -> {
            URI uri = exchange.getRequestURI();
            String body = new String(exchange.getRequestBody().readAllBytes(), StandardCharsets.UTF_8);
            requests.add(new Request(exchange.getRequestMethod(), uri.getRawPath(), uri.getRawQuery(), body));
            // As the coordinator answers a task it does not know.
            boolean unknown = uri.getRawPath().equals("/coordinator/tasks/t-gone");
            byte[] answer = (unknown ? "{\"code\":404,\"message\":\"No task with ID \\\"t-gone\\\" exists.\"}" : "{}")
                    .getBytes(StandardCharsets.UTF_8);
            exchange.getResponseHeaders().add("Content-Type", "application/json");
            exchange.sendResponseHeaders(unknown ? 404 : 200, answer.length);
            try (OutputStream out = exchange.getResponseBody()) {
                out.write(answer);
            }
        });
        coordinator.start();
        client = QuarkusRestClientBuilder.newBuilder()
                .baseUri(URI.create(
                        "http://127.0.0.1:" + coordinator.getAddress().getPort()))
                .build(TrogdorClient.class);
    }

    @AfterEach
    void stopCoordinator() throws Exception {
        if (client instanceof AutoCloseable closeable) {
            closeable.close();
        }
        coordinator.stop(0);
    }

    private Request only() {
        assertEquals(1, requests.size(), requests.toString());
        return requests.get(0);
    }

    @Test
    void getTaskReadsTheTaskUnderTasks() {
        client.getTask("run-1-produce-0");

        assertEquals(new Request("GET", "/coordinator/tasks/run-1-produce-0", null, ""), only());
    }

    @Test
    void stopTaskPutsTheIdInTheBody() throws IOException {
        client.stopTask(new TrogdorClient.StopTaskRequest("run-1-produce-0"));

        Request stop = only();
        assertEquals("PUT", stop.method());
        assertEquals("/coordinator/task/stop", stop.path());
        assertNull(stop.query());
        assertEquals(JSON.readTree("{\"id\": \"run-1-produce-0\"}"), JSON.readTree(stop.body()));
    }

    @Test
    void destroyTaskNamesTheTaskInTheQuery() {
        client.destroyTask("run-1-produce-0");

        assertEquals(new Request("DELETE", "/coordinator/tasks", "taskId=run-1-produce-0", ""), only());
    }

    @Test
    void getTasksSendsTheCoordinatorsFiltersAndLeavesOutTheUnset() {
        client.getTasks(List.of("a", "b"), 1_790_000_000_000L, null, null, null, "RUNNING");

        Request list = only();
        assertEquals("GET", list.method());
        assertEquals("/coordinator/tasks", list.path());
        assertEquals(
                List.of("taskId=a", "taskId=b", "firstStartMs=1790000000000", "state=RUNNING"),
                List.of(list.query().split("&")));
    }

    @Test
    void aSubmittedTaskIsPostedWithItsAgentAndTopic() {
        TrogdorBackend backend = new TrogdorBackend(client, List.of("node0"));
        BenchmarkTask task = BenchmarkTask.builder("run-1-produce-0", BenchmarkTask.WorkloadType.PRODUCE)
                .topic("perf")
                .partitions(6)
                .producerConfig(Map.of("bootstrap.servers", "broker:9092"))
                .build();

        backend.submit(task);

        Request create = only();
        assertEquals("POST", create.method());
        assertEquals("/coordinator/task/create", create.path());
        JsonNode sent = assertDoesNotThrow(() -> JSON.readTree(create.body()));
        assertEquals("run-1-produce-0", sent.path("id").asText());
        JsonNode spec = sent.path("spec");
        assertEquals(
                "org.apache.kafka.trogdor.workload.ProduceBenchSpec",
                spec.path("class").asText());
        assertEquals("node0", spec.path("producerNode").asText());
        assertEquals(
                6, spec.path("activeTopics").path("perf").path("numPartitions").asInt());
        assertEquals("constant", spec.path("valueGenerator").path("type").asText());
        assertFalse(spec.has("totalProducers"));
    }

    @Test
    void aTaskTheCoordinatorAnswers404ForHasFailed() {
        TrogdorBackend backend = new TrogdorBackend(client, List.of("node0"));

        BenchmarkStatus status = backend.poll(new BenchmarkHandle("trogdor", "t-gone"));

        assertEquals(TaskStatus.FAILED, status.getState());
        assertEquals("The Trogdor coordinator has no task t-gone", status.getError());
    }
}
