package com.bmscomp.kates.schedule;

import static org.hamcrest.Matchers.*;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyInt;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.ArgumentMatchers.eq;

import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.logging.Handler;
import java.util.logging.Level;
import java.util.logging.LogRecord;
import java.util.stream.Stream;
import jakarta.inject.Inject;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import io.restassured.RestAssured;
import io.restassured.http.ContentType;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.jboss.logmanager.ExtLogRecord;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.Arguments;
import org.junit.jupiter.params.provider.EnumSource;
import org.junit.jupiter.params.provider.MethodSource;
import org.mockito.ArgumentCaptor;
import org.mockito.Mockito;

import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.TopicService;
import com.bmscomp.kates.trogdor.TrogdorClient;

@QuarkusTest
class ScheduleResourceTest {

    @InjectMock
    ScheduledTestRunRepository repository;

    @InjectMock
    @RestClient
    TrogdorClient trogdorClient;

    @InjectMock
    TopicService topicService;

    @Inject
    TestScheduler scheduler;

    /** Held so the logger, and the handler on it, outlive the test's own references. */
    private final java.util.logging.Logger schedulerLog =
            java.util.logging.Logger.getLogger(TestScheduler.class.getName());

    /** What the scheduler logged at ERROR during the test. */
    private final List<String> schedulerErrors = new CopyOnWriteArrayList<>();

    private final Handler captureErrors = new Handler() {
        @Override
        public void publish(LogRecord record) {
            if (record.getLevel().intValue() >= Level.SEVERE.intValue()) {
                schedulerErrors.add(
                        record instanceof ExtLogRecord ext ? ext.getFormattedMessage() : record.getMessage());
            }
        }

        @Override
        public void flush() {}

        @Override
        public void close() {}
    };

    @BeforeEach
    void captureSchedulerErrors() {
        schedulerLog.addHandler(captureErrors);
    }

    @AfterEach
    void releaseSchedulerLog() {
        schedulerLog.removeHandler(captureErrors);
    }

    @Test
    void listSchedulesReturnsAll() {
        ScheduledTestRun s = new ScheduledTestRun();
        s.setId("s1");
        s.setName("nightly");
        s.setCronExpression("0 0 * * *");
        s.setEnabled(true);
        Mockito.when(repository.findAll()).thenReturn(List.of(s));

        var response = RestAssured.given()
                .when()
                .get("/api/schedules")
                .then()
                .statusCode(200)
                .extract()
                .body()
                .jsonPath();

        assertEquals(1, response.getList("").size());
    }

    @Test
    void getScheduleReturns404ForMissing() {
        Mockito.when(repository.findById("missing")).thenReturn(Optional.empty());

        RestAssured.given().when().get("/api/schedules/missing").then().statusCode(404);
    }

    @Test
    void getScheduleReturns200() {
        ScheduledTestRun s = new ScheduledTestRun();
        s.setId("s1");
        s.setName("nightly");
        Mockito.when(repository.findById("s1")).thenReturn(Optional.of(s));

        RestAssured.given().when().get("/api/schedules/s1").then().statusCode(200);
    }

    @Test
    void createScheduleRejects400WhenNameMissing() {
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"cronExpression\":\"0 0 * * *\",\"testRequest\":{\"type\":\"LOAD\"}}")
                .when()
                .post("/api/schedules")
                .then()
                .statusCode(400);
    }

    @Test
    void createScheduleRejects400WhenCronMissing() {
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"name\":\"test\",\"testRequest\":{\"type\":\"LOAD\"}}")
                .when()
                .post("/api/schedules")
                .then()
                .statusCode(400);
    }

    @Test
    void createScheduleReturns201() {
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"name\":\"nightly\",\"cronExpression\":\"0 0 * * *\",\"testRequest\":{\"type\":\"LOAD\"}}")
                .when()
                .post("/api/schedules")
                .then()
                .statusCode(201);

        Mockito.verify(repository).save(Mockito.any(ScheduledTestRun.class));
    }

    /**
     * A request that TestOrchestrator.refusal says the Kates API would not
     * run is refused before the schedule is saved, each field named by its
     * path in the schedule. It used to be saved anyway, and each firing then
     * failed with the reason in the server log only, and started no run.
     */
    @ParameterizedTest(name = "{1}")
    @MethodSource("refusedRequests")
    void aTestRequestTheApiWouldRefuseIsNotSaved(String testRequest, String field, String reason) {
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"name\":\"nightly\",\"cronExpression\":\"0 0 * * *\",\"testRequest\":" + testRequest + "}")
                .when()
                .post("/api/schedules")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body("fieldErrors", hasEntry(is(field), containsString(reason)))
                .body("message", containsString(field + ": " + reason));

        Mockito.verify(repository, Mockito.never()).save(any());
    }

    static Stream<Arguments> refusedRequests() {
        return Stream.of(
                Arguments.of(
                        "{\"type\":\"STRESS\",\"spec\":{\"consumerGroup\":\"perf-cg\"}}",
                        "testRequest.spec.consumerGroup",
                        "STRESS starts no consumer"),
                Arguments.of(
                        "{\"type\":\"LOAD\",\"spec\":{\"durationMs\":7200001}}",
                        "testRequest.spec.durationMs",
                        "the run is set to last 7200001 ms"),
                Arguments.of(
                        "{\"type\":\"LOAD\",\"scenario\":{\"phases\":[{\"name\":\"up\",\"phaseType\":\"RAMP\","
                                + "\"durationMs\":60000,\"rampSteps\":4}]}}",
                        "testRequest.scenario.phases[0].targetThroughput",
                        "a RAMP phase needs a rate"),
                // A scenario's specs are held to the limits the request's own
                // spec is; bean validation stops at the request's own.
                Arguments.of(
                        "{\"type\":\"LOAD\",\"scenario\":{\"baseSpec\":{\"acks\":\"2\"},\"phases\":[{\"name\":\"steady\","
                                + "\"phaseType\":\"STEADY\",\"durationMs\":60000}]}}",
                        "testRequest.scenario.baseSpec.acks",
                        "acks must be one of: all, -1, 0, 1"),
                Arguments.of(
                        "{\"type\":\"LOAD\",\"scenario\":{\"phases\":[{\"name\":\"steady\",\"phaseType\":\"STEADY\","
                                + "\"durationMs\":60000,\"spec\":{\"topic\":\"not a topic!\"}}]}}",
                        "testRequest.scenario.phases[0].spec.topic",
                        "topic must be a legal Kafka topic name"));
    }

    /**
     * A PUT's bean validation, like a POST's, stops at the request's own
     * spec, but a scenario's specs are held to their limits all the same: the
     * check is the one the Kates API asks of every request before it runs it.
     */
    @Test
    void aPutsScenarioSpecsAreHeldToTheirLimits() {
        ScheduledTestRun s = storedSchedule("{\"type\":\"LOAD\"}");
        Mockito.when(repository.findById("s1")).thenReturn(Optional.of(s));

        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"testRequest\":{\"type\":\"LOAD\",\"scenario\":{\"phases\":[{\"name\":\"steady\","
                        + "\"phaseType\":\"STEADY\",\"durationMs\":60000,\"spec\":{\"numRecords\":0}}]}}}")
                .when()
                .put("/api/schedules/s1")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body("fieldErrors", aMapWithSize(1))
                .body("fieldErrors", hasKey("testRequest.scenario.phases[0].spec.numRecords"));
        Mockito.verify(repository, Mockito.never()).save(any());
        assertEquals("{\"type\":\"LOAD\"}", s.getRequestJson());
    }

    /**
     * A PUT's testRequest gets the same check: a refused one leaves the
     * schedule as it was, and one the Kates API would run replaces it.
     */
    @Test
    void aPutsTestRequestIsCheckedTheSameWay() {
        ScheduledTestRun s = storedSchedule("{\"type\":\"LOAD\"}");
        Mockito.when(repository.findById("s1")).thenReturn(Optional.of(s));

        RestAssured.given()
                .contentType(ContentType.JSON)
                .body(
                        "{\"name\":\"renamed\",\"testRequest\":{\"type\":\"STRESS\",\"spec\":{\"consumerGroup\":\"perf-cg\"}}}")
                .when()
                .put("/api/schedules/s1")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body(
                        "fieldErrors",
                        hasEntry(is("testRequest.spec.consumerGroup"), containsString("STRESS starts no consumer")));
        Mockito.verify(repository, Mockito.never()).save(any());
        assertEquals("nightly", s.getName());
        assertEquals("{\"type\":\"LOAD\"}", s.getRequestJson());

        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"name\":\"renamed\",\"testRequest\":{\"type\":\"STRESS\",\"spec\":{\"numRecords\":1000}}}")
                .when()
                .put("/api/schedules/s1")
                .then()
                .statusCode(200)
                .body("name", is("renamed"));
        Mockito.verify(repository).save(s);
        assertTrue(s.getRequestJson().contains("\"numRecords\":1000"), s.getRequestJson());
    }

    /**
     * A PUT that sends no testRequest leaves the stored one unchecked, so a
     * schedule saved before the check, whose firings the Kates API refuses,
     * can still be disabled or renamed.
     */
    @Test
    void aPutWithoutATestRequestDoesNotCheckTheStoredOne() {
        ScheduledTestRun s = storedSchedule("{\"type\":\"STRESS\",\"spec\":{\"consumerGroup\":\"perf-cg\"}}");
        Mockito.when(repository.findById("s1")).thenReturn(Optional.of(s));

        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"enabled\":false}")
                .when()
                .put("/api/schedules/s1")
                .then()
                .statusCode(200)
                .body("enabled", is(false));

        Mockito.verify(repository).save(s);
    }

    /**
     * A PUT's testRequest is held to the bean constraints a POST's is, and
     * gets the body a POST gets for it. A PUT ran no bean validation, so it
     * saved what a POST refuses, such as STRESS with 1000 producers, which
     * each firing then started, and it keyed a missing type otherwise than a
     * POST does. The default messages depend on the JVM's locale, so the
     * bodies are compared rather than spelled out.
     */
    @ParameterizedTest(name = "{1}")
    @MethodSource("requestsBreakingTheirConstraints")
    void aPutsTestRequestGetsTheBeanValidationAPostsGets(String testRequest, String field) {
        ScheduledTestRun s = storedSchedule("{\"type\":\"LOAD\"}");
        Mockito.when(repository.findById("s1")).thenReturn(Optional.of(s));

        Map<String, Object> post = RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"name\":\"nightly\",\"cronExpression\":\"0 0 * * *\",\"testRequest\":" + testRequest + "}")
                .when()
                .post("/api/schedules")
                .then()
                .statusCode(400)
                .extract()
                .jsonPath()
                .getMap("");
        Map<String, Object> put = RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"name\":\"renamed\",\"testRequest\":" + testRequest + "}")
                .when()
                .put("/api/schedules/s1")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body("fieldErrors", hasKey(field))
                .extract()
                .jsonPath()
                .getMap("");

        assertEquals(post, put);
        Mockito.verify(repository, Mockito.never()).save(any());
        assertEquals("nightly", s.getName());
        assertEquals("{\"type\":\"LOAD\"}", s.getRequestJson());
    }

    static Stream<Arguments> requestsBreakingTheirConstraints() {
        return Stream.of(
                Arguments.of("{\"type\":\"STRESS\",\"spec\":{\"numProducers\":1000}}", "numProducers"),
                Arguments.of("{\"type\":\"LOAD\",\"spec\":{\"topic\":\"not a topic!\"}}", "topic"),
                Arguments.of("{\"spec\":{\"numRecords\":10}}", "type"),
                // A POST requires the request's own type, even beside a
                // scenario's, so a PUT does too.
                Arguments.of(
                        "{\"scenario\":{\"type\":\"LOAD\",\"phases\":[{\"name\":\"steady\",\"phaseType\":\"STEADY\","
                                + "\"durationMs\":60000}]}}",
                        "type"));
    }

    /**
     * A backend the Kates API doesn't have is refused before the schedule is
     * saved, keyed by the path of the backend the run would take. Such a
     * schedule was saved, and each firing then failed on the backend and
     * started no run, with the reason in the server log only.
     */
    @ParameterizedTest(name = "{1}")
    @MethodSource("unknownBackends")
    void aBackendTheApiDoesNotHaveIsNotSaved(String testRequest, String field) {
        ScheduledTestRun s = storedSchedule("{\"type\":\"LOAD\"}");
        Mockito.when(repository.findById("s1")).thenReturn(Optional.of(s));
        String reason = "the Kates API has no backend 'nope'; set it to native or trogdor";

        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"name\":\"nightly\",\"cronExpression\":\"0 0 * * *\",\"testRequest\":" + testRequest + "}")
                .when()
                .post("/api/schedules")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body("fieldErrors", is(Map.of(field, reason)))
                .body("message", is(field + ": " + reason));
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"testRequest\":" + testRequest + "}")
                .when()
                .put("/api/schedules/s1")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body("fieldErrors", is(Map.of(field, reason)));

        Mockito.verify(repository, Mockito.never()).save(any());
        assertEquals("{\"type\":\"LOAD\"}", s.getRequestJson());
    }

    static Stream<Arguments> unknownBackends() {
        String phases = "\"phases\":[{\"name\":\"steady\",\"phaseType\":\"STEADY\",\"durationMs\":60000}]";
        return Stream.of(
                Arguments.of("{\"type\":\"LOAD\",\"backend\":\"nope\"}", "testRequest.backend"),
                // A scenario runs on the request's backend unless it names
                // one of its own.
                Arguments.of(
                        "{\"type\":\"LOAD\",\"backend\":\"nope\",\"scenario\":{" + phases + "}}",
                        "testRequest.backend"),
                Arguments.of(
                        "{\"type\":\"LOAD\",\"scenario\":{\"backend\":\"nope\"," + phases + "}}",
                        "testRequest.scenario.backend"));
    }

    /**
     * A schedule saved before the check still fails at each firing: the
     * Kates API refuses its request, and no run starts.
     */
    @Test
    void aStoredTestRequestTheApiRefusesStartsNoRun() {
        ScheduledTestRun s = storedSchedule(
                "{\"type\":\"STRESS\",\"backend\":\"trogdor\",\"spec\":{\"consumerGroup\":\"perf-cg\"}}");

        scheduler.executeSchedule(s);

        Mockito.verify(repository, Mockito.never()).updateLastRun(anyString(), anyString());
        Mockito.verifyNoInteractions(trogdorClient);
    }

    /**
     * So does one whose scenario has a spec value outside its limits: the
     * firing is refused before a run starts. Such a run used to start, and a
     * topic Kafka cannot create failed it. The Trogdor mock refuses every
     * task, so a run the check let through would fail at submission.
     */
    @Test
    void aStoredScenarioOutsideItsLimitsStartsNoRun() {
        Mockito.when(trogdorClient.createTask(any())).thenThrow(new IllegalStateException("no coordinator in tests"));
        ScheduledTestRun s = storedSchedule("{\"type\":\"LOAD\",\"backend\":\"trogdor\",\"scenario\":{\"phases\":["
                + "{\"name\":\"steady\",\"phaseType\":\"STEADY\",\"durationMs\":60000,"
                + "\"spec\":{\"topic\":\"not a topic!\"}}]}}");

        scheduler.executeSchedule(s);

        Mockito.verify(repository, Mockito.never()).updateLastRun(anyString(), anyString());
        Mockito.verifyNoInteractions(trogdorClient);
    }

    /**
     * A rate of 0, which both benchmark backends ran unthrottled, is refused
     * wherever a schedule's request comes in: POST and PUT hold the request to
     * TestSpec's limits, and so does each firing of a schedule saved while 0
     * passed. The orchestrator refuses a phase's own rate of 0 at firing.
     */
    @Test
    void aRateOfZeroIsRefusedOnPostOnPutAndAtEachFiring() {
        // A request the checks let through fails at submission and frees its permit.
        Mockito.when(trogdorClient.createTask(any())).thenThrow(new IllegalStateException("no coordinator in tests"));
        String zero = "{\"type\":\"LOAD\",\"backend\":\"trogdor\",\"spec\":{\"throughput\":0}}";
        String why = "throughput must be -1 (unlimited) or positive";

        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"name\":\"nightly\",\"cronExpression\":\"0 0 * * *\",\"testRequest\":" + zero + "}")
                .when()
                .post("/api/schedules")
                .then()
                .statusCode(400)
                .body("fieldErrors.throughput", is(why));

        ScheduledTestRun s = storedSchedule("{\"type\":\"LOAD\"}");
        Mockito.when(repository.findById("s1")).thenReturn(Optional.of(s));
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"testRequest\":" + zero + "}")
                .when()
                .put("/api/schedules/s1")
                .then()
                .statusCode(400)
                .body("fieldErrors.throughput", is(why));
        Mockito.verify(repository, Mockito.never()).save(any());
        assertEquals("{\"type\":\"LOAD\"}", s.getRequestJson());

        for (String stored : List.of(
                zero,
                "{\"type\":\"LOAD\",\"backend\":\"trogdor\",\"scenario\":{\"baseSpec\":{\"throughput\":0},"
                        + "\"phases\":[{\"name\":\"steady\",\"phaseType\":\"STEADY\",\"durationMs\":60000}]}}",
                "{\"type\":\"LOAD\",\"backend\":\"trogdor\",\"scenario\":{\"phases\":[{\"name\":\"steady\","
                        + "\"phaseType\":\"STEADY\",\"durationMs\":60000,\"targetThroughput\":0}]}}")) {
            scheduler.executeSchedule(storedSchedule(stored));
        }
        Mockito.verify(repository, Mockito.never()).updateLastRun(anyString(), anyString());
        Mockito.verify(trogdorClient, Mockito.never()).createTask(any());
    }

    private static ScheduledTestRun storedSchedule(String requestJson) {
        ScheduledTestRun s = new ScheduledTestRun();
        s.setId("s1");
        s.setName("nightly");
        s.setCronExpression("0 0 * * *");
        s.setRequestJson(requestJson);
        return s;
    }

    /**
     * A schedule fires the request it was given, and only that. The request
     * used to be stored through TestSpec's getters, which wrote every field at
     * its default, so each firing asked for all of them: the backend refused
     * enableCrc or the fetch settings for every type but INTEGRITY, and the
     * run's requestedSpec claimed fields nobody had set.
     */
    @ParameterizedTest
    @EnumSource(TestType.class)
    void aScheduleRunsTheRequestItWasGiven(TestType type) {
        Mockito.doNothing().when(topicService).createTopic(anyString(), anyInt(), anyInt(), any());
        Mockito.when(trogdorClient.createTask(any())).thenThrow(new IllegalStateException("no coordinator in tests"));

        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"name\":\"nightly\",\"cronExpression\":\"0 0 * * *\",\"testRequest\":{\"type\":\"" + type
                        + "\",\"backend\":\"trogdor\",\"spec\":{\"numRecords\":1000}}}")
                .when()
                .post("/api/schedules")
                .then()
                .statusCode(201);
        ArgumentCaptor<ScheduledTestRun> saved = ArgumentCaptor.forClass(ScheduledTestRun.class);
        Mockito.verify(repository).save(saved.capture());

        scheduler.executeSchedule(saved.getValue());

        ArgumentCaptor<String> runId = ArgumentCaptor.forClass(String.class);
        Mockito.verify(repository).updateLastRun(eq(saved.getValue().getId()), runId.capture());
        // Read over HTTP, as a client would: each request sees the row as the
        // run's own thread last wrote it.
        awaitFinished(runId.getValue());
        RestAssured.given()
                .when()
                .get("/api/tests/" + runId.getValue())
                .then()
                .statusCode(200)
                .body("requestedSpec", is(Map.of("numRecords", 1000)))
                .body("spec.numRecords", is(1000))
                .body("spec", not(hasKey("enableCrc")));
    }

    private static void awaitFinished(String id) {
        long deadline = System.currentTimeMillis() + 10_000;
        while (System.currentTimeMillis() < deadline) {
            String status = RestAssured.given()
                    .when()
                    .get("/api/tests/" + id)
                    .then()
                    .extract()
                    .path("status");
            if ("FAILED".equals(status) || "DONE".equals(status)) {
                return;
            }
            try {
                Thread.sleep(50);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                break;
            }
        }
        throw new AssertionError("run " + id + " never finished");
    }

    /**
     * A firing holds the request's spec to the limits POST /api/tests holds
     * it to, whether the run reads that spec or not, and starts no run for a
     * value outside them: it logs each one, as it logs any refused firing.
     * The run used to go ahead: a numProducers of 1000, which a PUT saved
     * unchecked, started 1000 Trogdor tasks at each firing.
     */
    @ParameterizedTest(name = "{1}")
    @MethodSource("specsOutsideTheLimits")
    void aStoredSpecOutsideItsLimitsStartsNoRun(String requestJson, String field) {
        // A run the check let through would fail here, at submission.
        Mockito.when(trogdorClient.createTask(any())).thenThrow(new IllegalStateException("no coordinator in tests"));

        scheduler.executeSchedule(storedSchedule(requestJson));

        Mockito.verify(repository, Mockito.never()).updateLastRun(anyString(), anyString());
        Mockito.verifyNoInteractions(trogdorClient);
        assertTrue(
                schedulerErrors.stream()
                        .anyMatch(e ->
                                e.startsWith("Failed to execute schedule 'nightly': ") && e.contains(field + ": ")),
                schedulerErrors.toString());
    }

    static Stream<Arguments> specsOutsideTheLimits() {
        return Stream.of(
                Arguments.of(
                        "{\"type\":\"STRESS\",\"backend\":\"trogdor\",\"spec\":{\"numProducers\":1000}}",
                        "spec.numProducers"),
                Arguments.of(
                        "{\"type\":\"LOAD\",\"backend\":\"trogdor\",\"spec\":{\"topic\":\"no spaces\"}}", "spec.topic"),
                Arguments.of(
                        "{\"type\":\"LOAD\",\"backend\":\"trogdor\",\"spec\":{\"numRecords\":0},\"scenario\":{\"phases\":"
                                + "[{\"name\":\"steady\",\"phaseType\":\"STEADY\",\"durationMs\":60000}]}}",
                        "spec.numRecords"));
    }

    /**
     * Only the request's spec is held to the limits, so a request within them
     * still fires. A schedule stored before the Kates API kept only the fields
     * a request sets holds every spec field at its old Java default, all within
     * the limits; the first case is its spec as migration V23 leaves it. A
     * scenario with a type of its own, and none in the request, runs as the
     * scenario's type, though bean validation of the whole request would
     * refuse it for the missing type.
     */
    @ParameterizedTest(name = "{0}")
    @MethodSource("requestsWithinTheLimits")
    void aStoredRequestWithinTheLimitsStillRuns(String name, String requestJson) {
        Mockito.when(trogdorClient.createTask(any())).thenThrow(new IllegalStateException("no coordinator in tests"));

        scheduler.executeSchedule(storedSchedule(requestJson));

        ArgumentCaptor<String> runId = ArgumentCaptor.forClass(String.class);
        Mockito.verify(repository).updateLastRun(eq("s1"), runId.capture());
        awaitFinished(runId.getValue());
    }

    static Stream<Arguments> requestsWithinTheLimits() {
        return Stream.of(
                Arguments.of(
                        "old Java defaults",
                        "{\"type\":\"LOAD\",\"backend\":\"trogdor\",\"spec\":{\"numRecords\":1000000,\"recordSize\":1024,"
                                + "\"throughput\":-1,\"acks\":\"all\",\"batchSize\":65536,\"lingerMs\":5,"
                                + "\"compressionType\":\"lz4\",\"numProducers\":1,\"numConsumers\":1,"
                                + "\"durationMs\":600000,\"replicationFactor\":3,\"partitions\":3,"
                                + "\"minInsyncReplicas\":2}}"),
                Arguments.of(
                        "a scenario's own type",
                        "{\"backend\":\"trogdor\",\"scenario\":{\"type\":\"LOAD\",\"phases\":"
                                + "[{\"name\":\"steady\",\"phaseType\":\"STEADY\",\"durationMs\":60000}]}}"));
    }

    @Test
    void deleteScheduleReturns204() {
        ScheduledTestRun s = new ScheduledTestRun();
        s.setId("s1");
        Mockito.when(repository.findById("s1")).thenReturn(Optional.of(s));

        RestAssured.given().when().delete("/api/schedules/s1").then().statusCode(204);

        Mockito.verify(repository).delete("s1");
    }

    @Test
    void deleteScheduleReturns404ForMissing() {
        Mockito.when(repository.findById("missing")).thenReturn(Optional.empty());

        RestAssured.given().when().delete("/api/schedules/missing").then().statusCode(404);
    }
}
