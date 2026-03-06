package com.bmscomp.kates.schedule;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import java.util.Optional;

import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.InjectMock;
import io.restassured.RestAssured;
import io.restassured.http.ContentType;
import org.junit.jupiter.api.Test;
import org.mockito.Mockito;

@QuarkusTest
class ScheduleResourceTest {

    @InjectMock
    ScheduledTestRunRepository repository;

    @Test
    void listSchedulesReturnsAll() {
        ScheduledTestRun s = new ScheduledTestRun();
        s.setId("s1");
        s.setName("nightly");
        s.setCronExpression("0 0 * * *");
        s.setEnabled(true);
        Mockito.when(repository.findAll()).thenReturn(List.of(s));

        var response = RestAssured.given()
                .when().get("/api/schedules")
                .then().statusCode(200)
                .extract().body().jsonPath();

        assertEquals(1, response.getList("").size());
    }

    @Test
    void getScheduleReturns404ForMissing() {
        Mockito.when(repository.findById("missing")).thenReturn(Optional.empty());

        RestAssured.given()
                .when().get("/api/schedules/missing")
                .then().statusCode(404);
    }

    @Test
    void getScheduleReturns200() {
        ScheduledTestRun s = new ScheduledTestRun();
        s.setId("s1");
        s.setName("nightly");
        Mockito.when(repository.findById("s1")).thenReturn(Optional.of(s));

        RestAssured.given()
                .when().get("/api/schedules/s1")
                .then().statusCode(200);
    }

    @Test
    void createScheduleRejects400WhenNameMissing() {
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"cronExpression\":\"0 0 * * *\",\"testRequest\":{\"type\":\"LOAD\"}}")
                .when().post("/api/schedules")
                .then().statusCode(400);
    }

    @Test
    void createScheduleRejects400WhenCronMissing() {
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"name\":\"test\",\"testRequest\":{\"type\":\"LOAD\"}}")
                .when().post("/api/schedules")
                .then().statusCode(400);
    }

    @Test
    void createScheduleReturns201() {
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"name\":\"nightly\",\"cronExpression\":\"0 0 * * *\",\"testRequest\":{\"type\":\"LOAD\"}}")
                .when().post("/api/schedules")
                .then().statusCode(201);

        Mockito.verify(repository).save(Mockito.any(ScheduledTestRun.class));
    }

    @Test
    void deleteScheduleReturns204() {
        ScheduledTestRun s = new ScheduledTestRun();
        s.setId("s1");
        Mockito.when(repository.findById("s1")).thenReturn(Optional.of(s));

        RestAssured.given()
                .when().delete("/api/schedules/s1")
                .then().statusCode(204);

        Mockito.verify(repository).delete("s1");
    }

    @Test
    void deleteScheduleReturns404ForMissing() {
        Mockito.when(repository.findById("missing")).thenReturn(Optional.empty());

        RestAssured.given()
                .when().delete("/api/schedules/missing")
                .then().statusCode(404);
    }
}
