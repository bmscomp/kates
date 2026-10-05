package com.bmscomp.kates.api;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.*;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.mockito.Mockito.*;

import java.util.List;
import jakarta.inject.Inject;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.EnumSource;
import org.mockito.ArgumentCaptor;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.service.TopicService;
import com.bmscomp.kates.trogdor.TrogdorClient;
import com.bmscomp.kates.trogdor.spec.ConsumeBenchSpec;
import com.bmscomp.kates.trogdor.spec.ProduceBenchSpec;

@QuarkusTest
class TestResourceTest {

    @InjectMock
    @RestClient
    TrogdorClient trogdorClient;

    @InjectMock
    TopicService topicService;

    @Inject
    TestRunRepository repository;

    @BeforeEach
    void setUp() {
        doNothing().when(topicService).createTopic(anyString(), anyInt(), anyInt(), any());
    }

    @Test
    void listTestsReturnsPagedResponse() {
        given().when()
                .get("/api/tests")
                .then()
                .statusCode(200)
                .body("items", notNullValue())
                .body("page", is(0))
                .body("size", is(50))
                .body("total", greaterThanOrEqualTo(0));
    }

    @Test
    void listTestsAcceptsPagination() {
        given().queryParam("page", 0)
                .queryParam("size", 10)
                .when()
                .get("/api/tests")
                .then()
                .statusCode(200)
                .body("page", is(0))
                .body("size", is(10));
    }

    @Test
    void listTestsWithInvalidTypeReturns400() {
        given().queryParam("type", "INVALID")
                .when()
                .get("/api/tests")
                .then()
                .statusCode(400)
                .body("error", is("Bad Request"))
                .body("message", containsString("INVALID"));
    }

    @Test
    void getTestTypesReturnsAllTypes() {
        given().when()
                .get("/api/tests/types")
                .then()
                .statusCode(200)
                .body("$", hasSize(com.bmscomp.kates.domain.TestType.values().length))
                .body(
                        "$",
                        hasItems(
                                "LOAD",
                                "STRESS",
                                "SPIKE",
                                "ENDURANCE",
                                "VOLUME",
                                "CAPACITY",
                                "ROUND_TRIP",
                                "INTEGRITY",
                                "TUNE_REPLICATION",
                                "TUNE_ACKS",
                                "TUNE_BATCHING",
                                "TUNE_COMPRESSION",
                                "TUNE_PARTITIONS"));
    }

    @Test
    void createTestRequiresType() {
        given().contentType("application/json")
                .body("{}")
                .when()
                .post("/api/tests")
                .then()
                .statusCode(400);
    }

    @Test
    void getUnknownTestReturns404() {
        given().when().get("/api/tests/nonexistent").then().statusCode(404).body("error", is("Not Found"));
    }

    @Test
    void deleteUnknownTestReturns404() {
        given().when().delete("/api/tests/nonexistent").then().statusCode(404).body("error", is("Not Found"));
    }

    @Test
    void deleteRemovesTheRunWithItsResults() {
        var result = new TestResult().withTaskId("t-done").withStatus(TestResult.TaskStatus.DONE);
        var run = new TestRun(TestType.LOAD, null)
                .withStatus(TestResult.TaskStatus.DONE)
                .withBackend("native")
                .withResults(List.of(result));
        repository.save(run);

        given().when().delete("/api/tests/" + run.getId()).then().statusCode(204);

        given().when().get("/api/tests/" + run.getId()).then().statusCode(404);
        given().when().delete("/api/tests/" + run.getId()).then().statusCode(404);
    }

    @Test
    void createTestReturns202Accepted() {
        com.fasterxml.jackson.databind.ObjectMapper mapper = new com.fasterxml.jackson.databind.ObjectMapper();
        when(trogdorClient.createTask(any())).thenReturn(mapper.createObjectNode());

        given().contentType("application/json")
                .body("{\"type\": \"ROUND_TRIP\", \"spec\": {}}")
                .when()
                .post("/api/tests")
                .then()
                .statusCode(202)
                .body("testType", is("ROUND_TRIP"))
                .body("id", notNullValue())
                .body("status", is("PENDING"));
    }

    /**
     * A request's own fields reach the run, the effective spec shows them, and
     * the request is kept beside it. The Trogdor mock refuses every task, so the
     * run fails at submission and gives back its concurrency permit at once.
     */
    @Test
    void requestedFieldsReachTheRunAndTheRequestIsKept() {
        when(trogdorClient.createTask(any())).thenThrow(new IllegalStateException("no coordinator in tests"));

        String id = given().contentType("application/json")
                .body("{\"type\": \"LOAD\", \"backend\": \"trogdor\", \"spec\": {"
                        + "\"targetThroughput\": 2000, \"consumerGroup\": \"perf-cg\","
                        + " \"fetchMinBytes\": 65536, \"enableIdempotence\": false}}")
                .when()
                .post("/api/tests")
                .then()
                .statusCode(202)
                .body("spec.throughput", is(2000))
                .body("spec.targetThroughput", is(2000))
                .body("spec.consumerGroup", is("perf-cg"))
                .body("spec.fetchMinBytes", is(65536))
                .body("spec.enableIdempotence", is(false))
                .body("spec.numRecords", is(1000000))
                .body("requestedSpec.size()", is(4))
                .body("requestedSpec.targetThroughput", is(2000))
                .body("requestedSpec.enableIdempotence", is(false))
                .extract()
                .path("id");

        // What the backend was handed: the rate, the group and the client settings.
        var captor = ArgumentCaptor.forClass(TrogdorClient.CreateTaskRequest.class);
        verify(trogdorClient, timeout(5000).times(2)).createTask(captor.capture());
        var produce = (ProduceBenchSpec) captor.getAllValues().get(0).getSpec();
        var consume = (ConsumeBenchSpec) captor.getAllValues().get(1).getSpec();
        assertEquals(2000, produce.getTargetMessagesPerSec());
        assertEquals("false", produce.getProducerConf().get("enable.idempotence"));
        assertEquals("perf-cg", consume.getConsumerGroup());
        assertEquals("65536", consume.getConsumerConf().get("fetch.min.bytes"));

        awaitStatus(id, "FAILED");
        given().when()
                .get("/api/tests/" + id)
                .then()
                .statusCode(200)
                .body("spec.consumerGroup", is("perf-cg"))
                .body("requestedSpec.size()", is(4))
                .body("requestedSpec.consumerGroup", is("perf-cg"))
                .body("requestedSpec.fetchMinBytes", is(65536))
                .body("requestedSpec", not(hasKey("numRecords")));
    }

    @Test
    void aRequestWithoutASpecKeepsAnEmptyOne() {
        when(trogdorClient.createTask(any())).thenThrow(new IllegalStateException("no coordinator in tests"));

        String id = given().contentType("application/json")
                .body("{\"type\": \"VOLUME\", \"backend\": \"trogdor\"}")
                .when()
                .post("/api/tests")
                .then()
                .statusCode(202)
                .extract()
                .path("id");

        awaitStatus(id, "FAILED");
        given().when()
                .get("/api/tests/" + id)
                .then()
                .statusCode(200)
                .body("requestedSpec", anEmptyMap())
                .body("spec.recordSize", is(10240));
    }

    @Test
    void aRunStoredBeforeTheRequestWasKeptHasNone() {
        TestRun old = new TestRun(TestType.LOAD, new TestSpec()).withStatus(TestResult.TaskStatus.DONE);
        repository.save(old);

        given().when()
                .get("/api/tests/" + old.getId())
                .then()
                .statusCode(200)
                .body("spec", notNullValue())
                .body("$", not(hasKey("requestedSpec")));
    }

    @Test
    void aFieldTheTypeCannotApplyIsRefusedByName() {
        given().contentType("application/json")
                .body(
                        "{\"type\": \"STRESS\", \"backend\": \"trogdor\", \"spec\": {\"consumerGroup\": \"perf-cg\", \"enableCrc\": true}}")
                .when()
                .post("/api/tests")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body("fieldErrors.consumerGroup", containsString("STRESS starts no consumer"))
                .body("fieldErrors.enableCrc", containsString("only an INTEGRITY run"))
                .body("message", containsString("spec.consumerGroup"));
        verifyNoInteractions(trogdorClient);
    }

    /**
     * The spec a run shows is valid input: sent back as a request, the way
     * kates replay did with every run, it starts a run of the same type. It
     * used to carry every field at its Java default, enableCrc true and the
     * fetch settings among them, and the backend refused those for every type
     * but INTEGRITY.
     */
    @ParameterizedTest
    @EnumSource(TestType.class)
    void theSpecARunShowsCanBeSentBack(TestType type) {
        when(trogdorClient.createTask(any())).thenThrow(new IllegalStateException("no coordinator in tests"));

        String id = given().contentType("application/json")
                .body("{\"type\": \"" + type + "\", \"backend\": \"trogdor\", \"spec\": {\"numRecords\": 1000}}")
                .when()
                .post("/api/tests")
                .then()
                .statusCode(202)
                .extract()
                .path("id");
        awaitStatus(id, "FAILED");
        java.util.Map<String, Object> spec = given().when()
                .get("/api/tests/" + id)
                .then()
                .statusCode(200)
                .extract()
                .path("spec");

        String again = given().contentType("application/json")
                .body(java.util.Map.of("type", type.name(), "backend", "trogdor", "spec", spec))
                .when()
                .post("/api/tests")
                .then()
                .statusCode(202)
                .body("requestedSpec", is(spec))
                .extract()
                .path("id");
        awaitStatus(again, "FAILED");
    }

    /**
     * The timeout reaper fails a run that outlives its duration, and a run
     * set to last longer than kates.engine.max-duration-ms would outlive the
     * reaper's ceiling: it is refused rather than started and cut short.
     */
    @Test
    void aRunSetToLastLongerThanTheLimitIsRefused() {
        given().contentType("application/json")
                .body("{\"type\": \"LOAD\", \"backend\": \"trogdor\", \"spec\": {\"durationMs\": 7200001}}")
                .when()
                .post("/api/tests")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body("fieldErrors.durationMs", containsString("kates.engine.max-duration-ms"))
                .body("message", containsString("spec.durationMs: the run is set to last 7200001 ms"));
        verifyNoInteractions(trogdorClient);
    }

    @Test
    void anEmptyConsumerGroupIsRefused() {
        given().contentType("application/json")
                .body("{\"type\": \"LOAD\", \"backend\": \"trogdor\", \"spec\": {\"consumerGroup\": \" \"}}")
                .when()
                .post("/api/tests")
                .then()
                .statusCode(400)
                .body("fieldErrors.consumerGroup", notNullValue());
        verifyNoInteractions(trogdorClient);
    }

    /**
     * A scenario's base spec and phase specs are held to the limits the
     * request's own spec is, each value keyed by its path in the scenario, as
     * the fields its phases could not apply are. Bean validation stopped at
     * the request's own spec, so such a value went into the run: a numRecords
     * of 0 sent nothing, and a topic Kafka cannot create failed the run. The
     * Trogdor mock refuses every task, so a run the check let through would
     * fail at submission and give back its permit.
     */
    @Test
    void aScenarioSpecValueOutsideItsLimitsIsRefusedByItsPath() {
        when(trogdorClient.createTask(any())).thenThrow(new IllegalStateException("no coordinator in tests"));

        given().contentType("application/json")
                .body("{\"type\": \"LOAD\", \"backend\": \"trogdor\", \"scenario\": {\"baseSpec\": {\"acks\": \"2\"},"
                        + " \"phases\": [{\"name\": \"steady\", \"phaseType\": \"STEADY\", \"durationMs\": 60000,"
                        + " \"spec\": {\"numRecords\": 0, \"topic\": \"not a topic!\"}}]}}")
                .when()
                .post("/api/tests")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body("fieldErrors", aMapWithSize(3))
                .body("fieldErrors.'baseSpec.acks'", is("acks must be one of: all, -1, 0, 1"))
                .body("fieldErrors", hasKey("phases[0].spec.numRecords"))
                .body("fieldErrors.'phases[0].spec.topic'", is("topic must be a legal Kafka topic name"))
                .body(
                        "message",
                        org.hamcrest.Matchers.startsWith("scenario.baseSpec.acks: acks must be one of: all, -1, 0, 1;"
                                + " scenario.phases[0].spec.numRecords: "));
        verifyNoInteractions(trogdorClient);
    }

    /**
     * As with the request's own spec, a scenario's values outside their
     * limits are answered first, and on their own: the fields its phases could
     * not apply, here a SPIKE phase's rate and a consumer group no phase
     * reads, are named once the values are within them.
     */
    @Test
    void aScenarioSpecsLimitsAreCheckedBeforeWhatItsPhasesCouldNotApply() {
        given().contentType("application/json")
                .body("{\"type\": \"LOAD\", \"backend\": \"trogdor\", \"scenario\": {\"baseSpec\":"
                        + " {\"numRecords\": 0, \"consumerGroup\": \"perf-cg\"}, \"phases\": [{\"name\": \"burst\","
                        + " \"phaseType\": \"SPIKE\", \"durationMs\": 60000, \"spec\": {\"throughput\": 500}}]}}")
                .when()
                .post("/api/tests")
                .then()
                .statusCode(400)
                .body("fieldErrors", aMapWithSize(1))
                .body("fieldErrors", hasKey("baseSpec.numRecords"));
        verifyNoInteractions(trogdorClient);
    }

    /**
     * The request's own spec is still held to its limits by bean validation,
     * which answers first and keys each value by its field name, in a request
     * with a scenario too.
     */
    @Test
    void theRequestsOwnSpecIsStillKeyedByFieldName() {
        given().contentType("application/json")
                .body("{\"type\": \"LOAD\", \"backend\": \"trogdor\", \"spec\": {\"numRecords\": 0}, \"scenario\":"
                        + " {\"phases\": [{\"name\": \"steady\", \"phaseType\": \"STEADY\", \"durationMs\": 60000,"
                        + " \"spec\": {\"topic\": \"not a topic!\"}}]}}")
                .when()
                .post("/api/tests")
                .then()
                .statusCode(400)
                .body("message", is("Request validation failed"))
                .body("fieldErrors", aMapWithSize(1))
                .body("fieldErrors", hasKey("numRecords"));
        verifyNoInteractions(trogdorClient);
    }

    /** A scenario in a bulk request is held to the same limits, and refused in its own entry. */
    @Test
    void aBulkRequestsScenarioIsHeldToTheSameLimits() {
        when(trogdorClient.createTask(any())).thenThrow(new IllegalStateException("no coordinator in tests"));

        given().contentType("application/json")
                .body("[{\"type\": \"LOAD\", \"backend\": \"trogdor\", \"scenario\": {\"phases\": [{\"name\":"
                        + " \"steady\", \"phaseType\": \"STEADY\", \"durationMs\": 60000,"
                        + " \"spec\": {\"topic\": \"not a topic!\"}}]}}]")
                .when()
                .post("/api/tests/bulk")
                .then()
                .statusCode(202)
                .body("runs[0].id", nullValue())
                .body("runs[0].error", is("scenario.phases[0].spec.topic: topic must be a legal Kafka topic name"));
        verifyNoInteractions(trogdorClient);
    }

    private static void awaitStatus(String id, String status) {
        long deadline = System.currentTimeMillis() + 10_000;
        while (System.currentTimeMillis() < deadline) {
            String now = given().when().get("/api/tests/" + id).then().extract().path("status");
            if (status.equals(now)) {
                return;
            }
            try {
                Thread.sleep(50);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                return;
            }
        }
        throw new AssertionError("run " + id + " never reached " + status);
    }

    @Test
    void cancelAnswersTheStatusItStores() {
        // TaskStatus has no CANCELLED, so a cancelled run is stored FAILED.
        // The answer used to say CANCELLED all the same, and a client that
        // trusted it disagreed with every later read of the run.
        var running = new TestResult().withTaskId("t-running").withStatus(TestResult.TaskStatus.RUNNING);
        var done = new TestResult().withTaskId("t-done").withStatus(TestResult.TaskStatus.DONE);
        var run = new TestRun(TestType.LOAD, null)
                .withStatus(TestResult.TaskStatus.RUNNING)
                .withBackend("native")
                .withResults(List.of(running, done));
        repository.save(run);

        given().when()
                .post("/api/tests/" + run.getId() + "/cancel")
                .then()
                .statusCode(200)
                .body("id", is(run.getId()))
                .body("status", is("FAILED"))
                .body("reason", is("cancelled"));

        TestRun stored = repository.findById(run.getId()).orElseThrow();
        assertEquals(TestResult.TaskStatus.FAILED, stored.getStatus());
        TestResult stopped = stored.getResults().stream()
                .filter(r -> "t-running".equals(r.getTaskId()))
                .findFirst()
                .orElseThrow();
        assertEquals(TestResult.TaskStatus.FAILED, stopped.getStatus());
        assertEquals("Cancelled by user", stopped.getError());
        TestResult finished = stored.getResults().stream()
                .filter(r -> "t-done".equals(r.getTaskId()))
                .findFirst()
                .orElseThrow();
        assertEquals(TestResult.TaskStatus.DONE, finished.getStatus(), "a finished task is left as it ended");

        given().when().post("/api/tests/" + run.getId() + "/cancel").then().statusCode(409);
        given().when().post("/api/tests/no-such-run/cancel").then().statusCode(404);
    }
}
