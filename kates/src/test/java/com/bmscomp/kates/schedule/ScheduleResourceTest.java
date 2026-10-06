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
import java.util.stream.Stream;
import jakarta.inject.Inject;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import io.restassured.RestAssured;
import io.restassured.http.ContentType;
import org.eclipse.microprofile.rest.client.inject.RestClient;
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
                .statusCode(400)
                .body("message", is("name: name is required"));
    }

    @Test
    void createScheduleRejects400WhenCronMissing() {
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"name\":\"test\",\"testRequest\":{\"type\":\"LOAD\"}}")
                .when()
                .post("/api/schedules")
                .then()
                .statusCode(400)
                .body("message", is("cronExpression: cronExpression is required"));
    }

    /**
     * A testRequest value outside the limits POST /api/tests sets is named in
     * the message, with its reason. The message said only "Request
     * validation failed", so kates schedule create, which prints the message
     * alone, never said which field to change. The reason depends on the
     * JVM's locale, so it is read back from fieldErrors.
     */
    @Test
    void aTestRequestValueOutsideItsLimitsIsNamedInTheMessage() {
        var answer = RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"name\":\"nightly\",\"cronExpression\":\"0 0 * * *\","
                        + "\"testRequest\":{\"type\":\"STRESS\",\"spec\":{\"numProducers\":1000}}}")
                .when()
                .post("/api/schedules")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body("fieldErrors", aMapWithSize(1))
                .extract()
                .jsonPath();

        assertEquals("numProducers: " + answer.getString("fieldErrors.numProducers"), answer.getString("message"));
        Mockito.verify(repository, Mockito.never()).save(any());
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
                        "a RAMP phase needs a rate"));
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
