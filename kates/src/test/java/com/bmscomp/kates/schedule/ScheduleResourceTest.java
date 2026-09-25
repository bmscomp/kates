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
import jakarta.inject.Inject;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import io.restassured.RestAssured;
import io.restassured.http.ContentType;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.EnumSource;
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
