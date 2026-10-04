package com.bmscomp.kates.report;

import static org.hamcrest.Matchers.containsString;
import static org.hamcrest.Matchers.equalTo;
import static org.junit.jupiter.api.Assertions.*;

import java.util.Optional;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import io.restassured.RestAssured;
import io.restassured.http.ContentType;
import org.junit.jupiter.api.Test;
import org.mockito.Mockito;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.TestRunRepository;

@QuarkusTest
class ReportResourceTest {

    @InjectMock
    TestRunRepository repository;

    @InjectMock
    ReportGenerator generator;

    @Test
    void getReportReturns200() {
        TestRun run = new TestRun(TestType.LOAD, null).withId("r1");
        Mockito.when(repository.findById("r1")).thenReturn(Optional.of(run));
        Mockito.when(generator.generate(run)).thenReturn(new TestReport());

        RestAssured.given().when().get("/api/tests/r1/report").then().statusCode(200);
    }

    @Test
    void getReportReturnsErrorForMissingRun() {
        Mockito.when(repository.findById("missing")).thenReturn(Optional.empty());

        int status = RestAssured.given()
                .when()
                .get("/api/tests/missing/report")
                .then()
                .extract()
                .statusCode();

        assertTrue(status >= 400, "Expected 4xx/5xx for missing run, got " + status);
    }

    @Test
    void getMarkdownReturnsErrorForMissing() {
        Mockito.when(repository.findById("missing")).thenReturn(Optional.empty());

        int status = RestAssured.given()
                .when()
                .get("/api/tests/missing/report/markdown")
                .then()
                .extract()
                .statusCode();

        assertTrue(status >= 400);
    }

    @Test
    void getSummaryReturnsErrorForMissing() {
        Mockito.when(repository.findById("missing")).thenReturn(Optional.empty());

        int status = RestAssured.given()
                .when()
                .get("/api/tests/missing/report/summary")
                .then()
                .extract()
                .statusCode();

        assertTrue(status >= 400);
    }

    @Test
    void getCsvReturnsErrorForMissing() {
        Mockito.when(repository.findById("missing")).thenReturn(Optional.empty());

        int status = RestAssured.given()
                .when()
                .get("/api/tests/missing/report/csv")
                .then()
                .extract()
                .statusCode();

        assertTrue(status >= 400);
    }

    @Test
    void getJunitReturnsErrorForMissing() {
        Mockito.when(repository.findById("missing")).thenReturn(Optional.empty());

        int status = RestAssured.given()
                .when()
                .get("/api/tests/missing/report/junit")
                .then()
                .extract()
                .statusCode();

        assertTrue(status >= 400);
    }

    @Test
    void junitAnswers409WhileTheRunIsInFlight() {
        // Its verdict passes until it finishes, so a suite exported now would
        // tell a CI job that ran too early that the run passed.
        TestRun run = new TestRun(TestType.LOAD, null).withId("r2").withStatus(TestResult.TaskStatus.RUNNING);
        Mockito.when(repository.findById("r2")).thenReturn(Optional.of(run));

        RestAssured.given()
                .when()
                .get("/api/tests/r2/report/junit")
                .then()
                .statusCode(409)
                .contentType(ContentType.JSON)
                .body("status", equalTo(409))
                .body("message", containsString("RUNNING"));

        Mockito.verify(generator, Mockito.never()).generate(Mockito.any());
    }

    @Test
    void junitExportsAFinishedRun() {
        TestRun run = new TestRun(TestType.LOAD, null).withId("r3").withStatus(TestResult.TaskStatus.FAILED);
        Mockito.when(repository.findById("r3")).thenReturn(Optional.of(run));
        Mockito.when(generator.generate(run)).thenReturn(new TestReport());

        RestAssured.given()
                .when()
                .get("/api/tests/r3/report/junit")
                .then()
                .statusCode(200)
                .body(containsString("<testsuite"));
    }

    @Test
    void getHeatmapReturnsErrorForMissing() {
        Mockito.when(repository.findById("missing")).thenReturn(Optional.empty());

        int status = RestAssured.given()
                .when()
                .get("/api/tests/missing/report/heatmap")
                .then()
                .extract()
                .statusCode();

        assertTrue(status >= 400);
    }

    @Test
    void compareRejectsMissingIds() {
        RestAssured.given().when().get("/api/tests/reports/compare").then().statusCode(400);
    }

    @Test
    void compareRequiresMinTwoIds() {
        RestAssured.given()
                .queryParam("ids", "one")
                .when()
                .get("/api/tests/reports/compare")
                .then()
                .statusCode(400);
    }
}
