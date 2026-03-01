package com.bmscomp.kates.disruption;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.*;

import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.Test;

@QuarkusTest
class DisruptionResourceTest {

    @Test
    void listTypesReturnsAllDisruptionTypes() {
        given().when()
                .get("/api/disruptions/types")
                .then()
                .statusCode(200)
                .body("$.size()", is(13));
    }

    @Test
    void listTypesContainsNewExperimentTypes() {
        given().when()
                .get("/api/disruptions/types")
                .then()
                .statusCode(200)
                .body("find { it.name == 'MEMORY_STRESS' }.description", notNullValue())
                .body("find { it.name == 'IO_STRESS' }.description", notNullValue())
                .body("find { it.name == 'DNS_ERROR' }.description", notNullValue());
    }

    @Test
    void listTypesContainsTypeAndDescription() {
        given().when()
                .get("/api/disruptions/types")
                .then()
                .statusCode(200)
                .body("[0].name", notNullValue())
                .body("[0].description", notNullValue());
    }

    @Test
    void executeDisruptionReturns400WithEmptyPlan() {
        given().contentType("application/json")
                .body("{}")
                .when()
                .post("/api/disruptions")
                .then()
                .statusCode(400);
    }
}
