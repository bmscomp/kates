package com.klster.kates.api;

import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.Test;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.*;

@QuarkusTest
class TestResourceTest {

    @Test
    void listTestsReturnsEmptyList() {
        given()
                .when().get("/api/tests")
                .then()
                .statusCode(200)
                .body("$", hasSize(greaterThanOrEqualTo(0)));
    }

    @Test
    void getTestTypesReturnsAllTypes() {
        given()
                .when().get("/api/tests/types")
                .then()
                .statusCode(200)
                .body("$", hasSize(7))
                .body("$", hasItems("LOAD", "STRESS", "SPIKE", "ENDURANCE",
                        "VOLUME", "CAPACITY", "ROUND_TRIP"));
    }

    @Test
    void createTestRequiresType() {
        given()
                .contentType("application/json")
                .body("{}")
                .when().post("/api/tests")
                .then()
                .statusCode(400)
                .body("error", is("type is required"));
    }

    @Test
    void getUnknownTestReturns404() {
        given()
                .when().get("/api/tests/nonexistent")
                .then()
                .statusCode(404);
    }
}
