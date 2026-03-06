package com.bmscomp.kates.api;

import static org.junit.jupiter.api.Assertions.*;

import io.quarkus.test.junit.QuarkusTest;
import io.restassured.RestAssured;
import io.restassured.http.ContentType;
import org.junit.jupiter.api.Test;

@QuarkusTest
class ProfileResourceTest {

    @Test
    void listReturns200() {
        var response = RestAssured.given()
                .when().get("/api/profiles")
                .then().statusCode(200)
                .extract().body().jsonPath();

        assertNotNull(response.getList("items"));
        assertEquals(0, response.getInt("page"));
    }

    @Test
    void getReturns404ForMissingProfile() {
        RestAssured.given()
                .when().get("/api/profiles/nonexistent")
                .then().statusCode(404);
    }

    @Test
    void saveRejects400WhenFieldsMissing() {
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{}")
                .when().post("/api/profiles")
                .then().statusCode(400);
    }

    @Test
    void saveReturns404ForMissingRun() {
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"name\":\"perf-1\",\"runId\":\"missing-run\"}")
                .when().post("/api/profiles")
                .then().statusCode(404);
    }

    @Test
    void deleteReturns404ForMissingProfile() {
        RestAssured.given()
                .when().delete("/api/profiles/nonexistent")
                .then().statusCode(404);
    }
}
