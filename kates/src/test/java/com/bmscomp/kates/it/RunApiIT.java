package com.bmscomp.kates.it;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.equalTo;
import static org.hamcrest.Matchers.hasItem;
import static org.hamcrest.Matchers.hasSize;
import static org.hamcrest.Matchers.is;
import static org.hamcrest.Matchers.notNullValue;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.time.Duration;
import java.util.List;
import java.util.Map;
import jakarta.inject.Inject;

import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.restassured.http.ContentType;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.TestRunRepository;

/**
 * The write side of {@code /api/tests} end to end: HTTP in, real broker work,
 * real rows out.
 *
 * <p>{@code TestResourceTest} asserts that a submission returns 202 with the
 * Trogdor client and the topic service mocked out — which proves the JAX-RS
 * binding and nothing else. Nothing currently checks that a submitted run
 * actually produces to a broker, that its results are persisted and readable
 * back through the API, or that the bulk and cancel endpoints do what their
 * response bodies claim.
 */
@QuarkusTest
@TestProfile(IntegrationTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
@QuarkusTestResource(value = KafkaTestResource.class, restrictToAnnotatedClass = true)
class RunApiIT {

    private static final Duration TERMINAL_TIMEOUT = Duration.ofSeconds(90);

    @Inject
    TestRunRepository repository;

    @Test
    void submittedRunProducesToTheBrokerAndIsReadableBack() {
        String topic = ItSupport.uniqueTopic("test-api-it");
        String id = given().contentType(ContentType.JSON)
                .body(volumeRequest(topic))
                .when()
                .post("/api/tests")
                .then()
                .statusCode(202)
                .body("id", notNullValue())
                .body("testType", equalTo("VOLUME"))
                .body("status", equalTo("PENDING"))
                .extract()
                .path("id");

        assertTrue(waitForTerminal(id), "run " + id + " never reached a terminal state");

        // The run finished against a real single-broker cluster, so the
        // persisted result must carry the records it actually acked — not the
        // zeroes a mocked backend would leave behind.
        given().when()
                .get("/api/tests/" + id)
                .then()
                .statusCode(200)
                .body("status", equalTo("DONE"))
                .body("results", hasSize(1))
                .body("results[0].recordsSent", is(25))
                .body("results[0].status", equalTo("DONE"));

        given().when().get("/api/tests?type=VOLUME").then().statusCode(200).body("items.id", hasItem(id));
    }

    @Test
    void bulkCreateSubmitsEveryItemAndReportsPerItemOutcomes() {
        List<Map<String, Object>> batch =
                List.of(volumeRequest(ItSupport.uniqueTopic("bulk-a")), volumeRequest(ItSupport.uniqueTopic("bulk-b")));

        List<String> ids = given().contentType(ContentType.JSON)
                .body(batch)
                .when()
                .post("/api/tests/bulk")
                .then()
                .statusCode(202)
                .body("created", is(2))
                .body("runs", hasSize(2))
                .extract()
                .jsonPath()
                .getList("runs.id", String.class);

        for (String id : ids) {
            assertTrue(waitForTerminal(id), "bulk run " + id + " never reached a terminal state");
            assertEquals(
                    "DONE",
                    given().when()
                            .get("/api/tests/" + id)
                            .then()
                            .statusCode(200)
                            .extract()
                            .path("status"),
                    "every accepted bulk item runs for real");
        }
    }

    @Test
    void bulkCreateEnforcesItsDocumentedBounds() {
        given().contentType(ContentType.JSON)
                .body(List.of())
                .when()
                .post("/api/tests/bulk")
                .then()
                .statusCode(400);

        // Eleven identical requests: the cap is checked before anything is
        // submitted, so this must not start a single run.
        List<Map<String, Object>> tooMany = java.util.Collections.nCopies(11, volumeRequest("bulk-overflow"));
        given().contentType(ContentType.JSON)
                .body(tooMany)
                .when()
                .post("/api/tests/bulk")
                .then()
                .statusCode(400);
    }

    @Test
    void bulkDeleteSeparatesDeletedFromNotFound() {
        // Seeded rather than submitted: this endpoint is about the delete
        // arithmetic, and starting real runs would only slow it down.
        String first = seedFinishedRun();
        String second = seedFinishedRun();

        given().contentType(ContentType.JSON)
                .body(Map.of("ids", List.of(first, second, "definitely-not-a-run")))
                .when()
                .delete("/api/tests/bulk")
                .then()
                .statusCode(200)
                .body("deleted", is(2))
                .body("notFound", is(1));

        given().when().get("/api/tests/" + first).then().statusCode(404);
    }

    @Test
    void cancelReportsCancelledButPersistsFailed() {
        // Seeded PENDING rather than submitted, on purpose. Cancelling a run
        // that was just POSTed races the submission itself: executeTest saves
        // PENDING, then a virtual thread creates the topic and saves RUNNING.
        // A cancel landing in that window writes FAILED and is then overwritten
        // by the RUNNING save, so the run finishes DONE. That race is worth
        // fixing in the orchestrator; it is not worth encoding as a flaky test,
        // and it is not what this test is about.
        String id = seedRun(TestResult.TaskStatus.PENDING);

        // The response body says CANCELLED but TaskStatus has no such constant
        // — the run is persisted as FAILED. Pinned deliberately: clients read
        // the body, dashboards read the row, and the two disagree.
        given().when()
                .post("/api/tests/" + id + "/cancel")
                .then()
                .statusCode(200)
                .body("id", equalTo(id))
                .body("status", equalTo("CANCELLED"));

        given().when().get("/api/tests/" + id).then().statusCode(200).body("status", equalTo("FAILED"));

        // Cancelling something already terminal is a conflict, not a no-op.
        given().when().post("/api/tests/" + id + "/cancel").then().statusCode(409);

        given().when().post("/api/tests/no-such-run/cancel").then().statusCode(404);
    }

    @Test
    void backendsEndpointReportsTheEnginesThatAreActuallyWired() {
        given().when().get("/api/tests/backends").then().statusCode(200).body("$", hasItem("native"));
    }

    // ----------------------------------------------------------------- helpers

    /** A VOLUME run: producer-only, so it finishes as soon as the records ack. */
    private static Map<String, Object> volumeRequest(String topic) {
        return Map.of(
                "type",
                "VOLUME",
                "backend",
                "native",
                "spec",
                new java.util.HashMap<String, Object>(Map.of(
                        "topic", topic,
                        "numRecords", 25,
                        "recordSize", 512,
                        "partitions", 1,
                        "replicationFactor", 1,
                        "minInsyncReplicas", 1,
                        "acks", "all",
                        "durationMs", 60_000)));
    }

    private boolean waitForTerminal(String id) {
        return ItSupport.waitUntil(TERMINAL_TIMEOUT, () -> {
            String status =
                    given().when().get("/api/tests/" + id).then().extract().path("status");
            return "DONE".equals(status) || "FAILED".equals(status);
        });
    }

    private String seedFinishedRun() {
        var run = ItSupport.finishedRun(TestType.LOAD, ItSupport.uniqueTopic("seeded"), 100, 1.0, 2.0);
        repository.save(run);
        return run.getId();
    }

    private String seedRun(TestResult.TaskStatus status) {
        var run = new com.bmscomp.kates.domain.TestRun(TestType.LOAD, ItSupport.singleBrokerSpec(null))
                .withStatus(status)
                .withBackend("native");
        repository.save(run);
        return run.getId();
    }
}
