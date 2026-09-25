package com.bmscomp.kates.api;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.*;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.mockito.Mockito.*;

import java.util.List;
import jakarta.inject.Inject;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.service.TopicService;
import com.bmscomp.kates.trogdor.TrogdorClient;

@QuarkusTest
class TestResourceTest {

    @InjectMock
    @RestClient
    TrogdorClient trogdorClient;

    @InjectMock
    TopicService topicService;

    @Inject
    TestRunRepository repository;

    @BeforeEach
    void setUp() {
        doNothing().when(topicService).createTopic(anyString(), anyInt(), anyInt(), any());
    }

    @Test
    void listTestsReturnsPagedResponse() {
        given().when()
                .get("/api/tests")
                .then()
                .statusCode(200)
                .body("items", notNullValue())
                .body("page", is(0))
                .body("size", is(50))
                .body("total", greaterThanOrEqualTo(0));
    }

    @Test
    void listTestsAcceptsPagination() {
        given().queryParam("page", 0)
                .queryParam("size", 10)
                .when()
                .get("/api/tests")
                .then()
                .statusCode(200)
                .body("page", is(0))
                .body("size", is(10));
    }

    @Test
    void listTestsWithInvalidTypeReturns400() {
        given().queryParam("type", "INVALID")
                .when()
                .get("/api/tests")
                .then()
                .statusCode(400)
                .body("error", is("Bad Request"))
                .body("message", containsString("INVALID"));
    }

    @Test
    void getTestTypesReturnsAllTypes() {
        given().when()
                .get("/api/tests/types")
                .then()
                .statusCode(200)
                .body("$", hasSize(com.bmscomp.kates.domain.TestType.values().length))
                .body(
                        "$",
                        hasItems(
                                "LOAD",
                                "STRESS",
                                "SPIKE",
                                "ENDURANCE",
                                "VOLUME",
                                "CAPACITY",
                                "ROUND_TRIP",
                                "INTEGRITY",
                                "TUNE_REPLICATION",
                                "TUNE_ACKS",
                                "TUNE_BATCHING",
                                "TUNE_COMPRESSION",
                                "TUNE_PARTITIONS"));
    }

    @Test
    void createTestRequiresType() {
        given().contentType("application/json")
                .body("{}")
                .when()
                .post("/api/tests")
                .then()
                .statusCode(400);
    }

    @Test
    void getUnknownTestReturns404() {
        given().when().get("/api/tests/nonexistent").then().statusCode(404).body("error", is("Not Found"));
    }

    @Test
    void deleteUnknownTestReturns404() {
        given().when().delete("/api/tests/nonexistent").then().statusCode(404).body("error", is("Not Found"));
    }

    @Test
    void createTestReturns202Accepted() {
        com.fasterxml.jackson.databind.ObjectMapper mapper = new com.fasterxml.jackson.databind.ObjectMapper();
        when(trogdorClient.createTask(any())).thenReturn(mapper.createObjectNode());

        given().contentType("application/json")
                .body("{\"type\": \"ROUND_TRIP\", \"spec\": {}}")
                .when()
                .post("/api/tests")
                .then()
                .statusCode(202)
                .body("testType", is("ROUND_TRIP"))
                .body("id", notNullValue())
                .body("status", is("PENDING"));
    }

    @Test
    void cancelAnswersTheStatusItStores() {
        // TaskStatus has no CANCELLED, so a cancelled run is stored FAILED.
        // The answer used to say CANCELLED all the same, and a client that
        // trusted it disagreed with every later read of the run.
        var running = new TestResult().withTaskId("t-running").withStatus(TestResult.TaskStatus.RUNNING);
        var done = new TestResult().withTaskId("t-done").withStatus(TestResult.TaskStatus.DONE);
        var run = new TestRun(TestType.LOAD, null)
                .withStatus(TestResult.TaskStatus.RUNNING)
                .withBackend("native")
                .withResults(List.of(running, done));
        repository.save(run);

        given().when()
                .post("/api/tests/" + run.getId() + "/cancel")
                .then()
                .statusCode(200)
                .body("id", is(run.getId()))
                .body("status", is("FAILED"))
                .body("reason", is("cancelled"));

        TestRun stored = repository.findById(run.getId()).orElseThrow();
        assertEquals(TestResult.TaskStatus.FAILED, stored.getStatus());
        TestResult stopped = stored.getResults().stream()
                .filter(r -> "t-running".equals(r.getTaskId()))
                .findFirst()
                .orElseThrow();
        assertEquals(TestResult.TaskStatus.FAILED, stopped.getStatus());
        assertEquals("Cancelled by user", stopped.getError());
        TestResult finished = stored.getResults().stream()
                .filter(r -> "t-done".equals(r.getTaskId()))
                .findFirst()
                .orElseThrow();
        assertEquals(TestResult.TaskStatus.DONE, finished.getStatus(), "a finished task is left as it ended");

        given().when().post("/api/tests/" + run.getId() + "/cancel").then().statusCode(409);
        given().when().post("/api/tests/no-such-run/cancel").then().statusCode(404);
    }
}
