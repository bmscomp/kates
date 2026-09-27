package com.bmscomp.kates.api;

import static org.junit.jupiter.api.Assertions.*;

import jakarta.inject.Inject;

import io.quarkus.test.junit.QuarkusTest;
import io.restassured.RestAssured;
import io.restassured.http.ContentType;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.TestRunRepository;

@QuarkusTest
class ProfileResourceTest {

    @Inject
    TestRunRepository repository;

    @Test
    void listReturns200() {
        var response = RestAssured.given()
                .when()
                .get("/api/profiles")
                .then()
                .statusCode(200)
                .extract()
                .body()
                .jsonPath();

        assertNotNull(response.getList("items"));
        assertEquals(0, response.getInt("page"));
    }

    @Test
    void getReturns404ForMissingProfile() {
        RestAssured.given().when().get("/api/profiles/nonexistent").then().statusCode(404);
    }

    @Test
    void saveRejects400WhenFieldsMissing() {
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{}")
                .when()
                .post("/api/profiles")
                .then()
                .statusCode(400);
    }

    @Test
    void saveReturns404ForMissingRun() {
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"name\":\"perf-1\",\"runId\":\"missing-run\"}")
                .when()
                .post("/api/profiles")
                .then()
                .statusCode(404);
    }

    @Test
    void saveTakesALoadRunsLatencyFromItsProducer() {
        // A LOAD run's consumer records no latency. Averaged in, its 0s halved
        // every latency the profile kept, and a later assert compared with that.
        TestRun run = new TestRun(TestType.LOAD, new TestSpec())
                .withStatus(TestResult.TaskStatus.DONE)
                .withAddedResult(new TestResult()
                        .withTaskId("profile-load-produce-0")
                        .withPhaseName("produce")
                        .withStatus(TestResult.TaskStatus.DONE)
                        .withRecordsSent(10_000)
                        .withThroughputRecordsPerSec(1_000)
                        .withAvgLatencyMs(12)
                        .withP50LatencyMs(8)
                        .withP95LatencyMs(25)
                        .withP99LatencyMs(40)
                        .withMaxLatencyMs(95))
                .withAddedResult(new TestResult()
                        .withTaskId("profile-load-consume-0")
                        .withPhaseName("consume")
                        .withStatus(TestResult.TaskStatus.DONE)
                        .withRecordsSent(10_000)
                        .withThroughputRecordsPerSec(900));
        repository.save(run);

        var profile = RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"name\":\"load-producer-latency\",\"runId\":\"" + run.getId() + "\"}")
                .when()
                .post("/api/profiles")
                .then()
                .statusCode(201)
                .extract()
                .body()
                .jsonPath();

        assertEquals(40.0, profile.getDouble("p99Ms"), 0.001);
        assertEquals(25.0, profile.getDouble("p95Ms"), 0.001);
        assertEquals(8.0, profile.getDouble("p50Ms"), 0.001);
        assertEquals(12.0, profile.getDouble("avgMs"), 0.001);
        assertEquals(950.0, profile.getDouble("throughput"), 0.001);
        assertEquals(20_000.0, profile.getDouble("records"), 0.001);

        RestAssured.given()
                .when()
                .delete("/api/profiles/load-producer-latency")
                .then()
                .statusCode(204);
    }

    @Test
    void deleteReturns404ForMissingProfile() {
        RestAssured.given().when().delete("/api/profiles/nonexistent").then().statusCode(404);
    }
}
