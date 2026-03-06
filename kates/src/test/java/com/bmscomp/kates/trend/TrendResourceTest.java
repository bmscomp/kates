package com.bmscomp.kates.trend;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;

import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.InjectMock;
import io.restassured.RestAssured;
import org.junit.jupiter.api.Test;
import org.mockito.Mockito;

import com.bmscomp.kates.domain.TestType;

@QuarkusTest
class TrendResourceTest {

    @InjectMock
    TrendService trendService;

    @Test
    void getTrendReturns400WhenTypeMissing() {
        RestAssured.given()
                .when().get("/api/trends")
                .then().statusCode(400);
    }

    @Test
    void getTrendReturns400ForInvalidType() {
        RestAssured.given()
                .queryParam("type", "INVALID")
                .when().get("/api/trends")
                .then().statusCode(400);
    }

    @Test
    void getTrendReturns400ForInvalidDays() {
        RestAssured.given()
                .queryParam("type", "LOAD")
                .queryParam("days", 0)
                .when().get("/api/trends")
                .then().statusCode(400);
    }

    @Test
    void getTrendReturns200() {
        TrendResponse response = TrendResponse.empty("LOAD", "avgThroughputRecPerSec");
        Mockito.when(trendService.computeTrend(TestType.LOAD, "avgThroughputRecPerSec", 30, 5, null))
                .thenReturn(response);

        RestAssured.given()
                .queryParam("type", "LOAD")
                .when().get("/api/trends")
                .then().statusCode(200);
    }

    @Test
    void getPhasesReturns200() {
        Mockito.when(trendService.discoverPhases(TestType.LOAD, 30)).thenReturn(List.of("warmup", "peak"));

        var body = RestAssured.given()
                .queryParam("type", "LOAD")
                .when().get("/api/trends/phases")
                .then().statusCode(200)
                .extract().body().jsonPath();

        assertEquals(2, body.getList("").size());
    }

    @Test
    void getBreakdownReturns200() {
        PhaseTrendResponse response = new PhaseTrendResponse("LOAD", "avgThroughputRecPerSec", List.of());
        Mockito.when(trendService.computeBreakdown(TestType.LOAD, "avgThroughputRecPerSec", 30, 5))
                .thenReturn(response);

        RestAssured.given()
                .queryParam("type", "LOAD")
                .when().get("/api/trends/breakdown")
                .then().statusCode(200);
    }

    @Test
    void getBrokerTrendReturns200() {
        BrokerTrendResponse response = new BrokerTrendResponse();
        Mockito.when(trendService.computeBrokerTrend(TestType.LOAD, "avgThroughputRecPerSec", 0, 30, 5))
                .thenReturn(response);

        RestAssured.given()
                .queryParam("type", "LOAD")
                .when().get("/api/trends/broker")
                .then().statusCode(200);
    }
}
