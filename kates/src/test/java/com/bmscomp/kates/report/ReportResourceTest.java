package com.bmscomp.kates.report;

import static org.junit.jupiter.api.Assertions.*;

import java.util.Optional;

import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.InjectMock;
import io.restassured.RestAssured;
import org.junit.jupiter.api.Test;
import org.mockito.Mockito;

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

        RestAssured.given()
                .when().get("/api/tests/r1/report")
                .then().statusCode(200);
    }

    @Test
    void getReportReturnsErrorForMissingRun() {
        Mockito.when(repository.findById("missing")).thenReturn(Optional.empty());

        int status = RestAssured.given()
                .when().get("/api/tests/missing/report")
                .then().extract().statusCode();

        assertTrue(status >= 400, "Expected 4xx/5xx for missing run, got " + status);
    }

    @Test
    void getMarkdownReturnsErrorForMissing() {
        Mockito.when(repository.findById("missing")).thenReturn(Optional.empty());

        int status = RestAssured.given()
                .when().get("/api/tests/missing/report/markdown")
                .then().extract().statusCode();

        assertTrue(status >= 400);
    }

    @Test
    void getSummaryReturnsErrorForMissing() {
        Mockito.when(repository.findById("missing")).thenReturn(Optional.empty());

        int status = RestAssured.given()
                .when().get("/api/tests/missing/report/summary")
                .then().extract().statusCode();

        assertTrue(status >= 400);
    }

    @Test
    void getCsvReturnsErrorForMissing() {
        Mockito.when(repository.findById("missing")).thenReturn(Optional.empty());

        int status = RestAssured.given()
                .when().get("/api/tests/missing/report/csv")
                .then().extract().statusCode();

        assertTrue(status >= 400);
    }

    @Test
    void getJunitReturnsErrorForMissing() {
        Mockito.when(repository.findById("missing")).thenReturn(Optional.empty());

        int status = RestAssured.given()
                .when().get("/api/tests/missing/report/junit")
                .then().extract().statusCode();

        assertTrue(status >= 400);
    }

    @Test
    void getHeatmapReturnsErrorForMissing() {
        Mockito.when(repository.findById("missing")).thenReturn(Optional.empty());

        int status = RestAssured.given()
                .when().get("/api/tests/missing/report/heatmap")
                .then().extract().statusCode();

        assertTrue(status >= 400);
    }

    @Test
    void compareRejectsMissingIds() {
        RestAssured.given()
                .when().get("/api/tests/reports/compare")
                .then().statusCode(400);
    }

    @Test
    void compareRequiresMinTwoIds() {
        RestAssured.given()
                .queryParam("ids", "one")
                .when().get("/api/tests/reports/compare")
                .then().statusCode(400);
    }
}
