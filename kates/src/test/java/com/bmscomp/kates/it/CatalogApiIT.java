package com.bmscomp.kates.it;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.equalTo;
import static org.hamcrest.Matchers.greaterThanOrEqualTo;
import static org.hamcrest.Matchers.hasItem;
import static org.hamcrest.Matchers.hasSize;
import static org.hamcrest.Matchers.is;
import static org.hamcrest.Matchers.notNullValue;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.List;
import java.util.Map;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;

import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.restassured.http.ContentType;
import io.restassured.response.Response;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.TestRunRepository;

/**
 * The read/query side of the REST API against real PostgreSQL.
 *
 * <p>The existing {@code @QuarkusTest} resource tests run on H2 with the
 * service layer mocked, so they can only assert the 400/404 branches — a
 * mocked repository has no rows to page through, no baseline to compare
 * against and no audit trail to read back. Everything here is asserted against
 * rows that actually went through Hibernate, Flyway-managed DDL and the JSON
 * columns, which is where the paging arithmetic, the baseline/regression maths
 * and the audit write-through actually live.
 *
 * <p>No broker is required: every endpoint under test reads persisted state.
 * Runs are seeded through the repository rather than {@code POST /api/tests}
 * so the fixtures are deterministic — {@link RunApiIT} covers the submit path.
 */
@QuarkusTest
@TestProfile(NoSchedulersTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
class CatalogApiIT {

    @Inject
    EntityManager em;

    @Inject
    TestRunRepository repository;

    @BeforeEach
    void resetCatalog() {
        // The container is per class, so absolute counts ("total == 5") are
        // only meaningful from a known-empty table.
        ItSupport.truncate(em, "test_runs", "outbox_events", "audit_events", "profiles", "baseline_runs");
    }

    // ---------------------------------------------------------------- listing

    @Test
    void listingPagesFiltersAndCountsAgainstRealRows() {
        seed(TestType.LOAD, TestResult.TaskStatus.DONE, 3);
        seed(TestType.STRESS, TestResult.TaskStatus.FAILED, 2);

        // total is the full row count; count is what this page carries. The two
        // diverge only when there is real pagination to do, which a mocked
        // repository never produces.
        given().when()
                .get("/api/tests?size=2")
                .then()
                .statusCode(200)
                .body("total", is(5))
                .body("count", is(2))
                .body("items", hasSize(2))
                .body("page", is(0));

        given().when()
                .get("/api/tests?type=LOAD")
                .then()
                .statusCode(200)
                .body("total", is(3))
                .body("items.testType", hasItem("LOAD"));

        given().when().get("/api/tests?status=FAILED").then().statusCode(200).body("total", is(2));

        // A caller asking for 10_000 rows gets the documented cap, not the
        // whole table.
        given().when().get("/api/tests?size=10000").then().statusCode(200).body("size", is(200));

        // type wins over status when both are supplied — pinned because the
        // combination silently ignores one filter rather than erroring.
        given().when()
                .get("/api/tests?type=LOAD&status=FAILED")
                .then()
                .statusCode(200)
                .body("total", is(3));
    }

    @Test
    void versionedAliasReachesTheSameRow() {
        String id = seedOne(TestType.LOAD, TestResult.TaskStatus.DONE);

        given().when().get("/api/v1/tests/" + id).then().statusCode(200).body("id", equalTo(id));
    }

    // -------------------------------------------------------------- baselines

    @Test
    void baselineLifecycleAndRegressionComparison() {
        // Baseline run is ten times faster than the candidate, so the
        // comparison has to detect a throughput regression.
        String fastId = seedRun(ItSupport.finishedRun(TestType.LOAD, null, 100_000, 5.0, 20.0));
        String slowId = seedRun(ItSupport.finishedRun(TestType.LOAD, null, 10_000, 5.0, 20.0));

        given().when().get("/api/tests/baselines/LOAD").then().statusCode(404);

        given().contentType(ContentType.JSON)
                .body(Map.of("runId", fastId))
                .when()
                .put("/api/tests/baselines/LOAD")
                .then()
                .statusCode(200)
                .body("testType", equalTo("LOAD"))
                .body("runId", equalTo(fastId))
                .body("setAt", notNullValue());

        given().when().get("/api/tests/baselines/LOAD").then().statusCode(200).body("runId", equalTo(fastId));

        given().when().get("/api/tests/baselines").then().statusCode(200).body("runId", hasItem(fastId));

        // The regression endpoint is the reason baselines exist and has no
        // coverage at all today.
        given().when()
                .get("/api/tests/" + slowId + "/report/regression")
                .then()
                .statusCode(200)
                .body("baselineId", equalTo(fastId))
                .body("testType", equalTo("LOAD"))
                .body("regressionDetected", is(true))
                .body("warnings", hasItem("Throughput dropped > 10%"))
                .body("deltas.avgThroughputRecPerSec.baseline", notNullValue())
                .body("deltas.avgThroughputRecPerSec.current", notNullValue());

        // Re-pointing the baseline is an upsert keyed on the test type, not a
        // second row.
        given().contentType(ContentType.JSON)
                .body(Map.of("runId", slowId))
                .when()
                .put("/api/tests/baselines/LOAD")
                .then()
                .statusCode(200);
        given().when().get("/api/tests/baselines").then().statusCode(200).body("$", hasSize(1));

        given().when().delete("/api/tests/baselines/LOAD").then().statusCode(204);
        given().when().delete("/api/tests/baselines/LOAD").then().statusCode(404);
        given().when().get("/api/tests/" + slowId + "/report/regression").then().statusCode(404);
    }

    @Test
    void baselineRejectsUnknownTypesAndRuns() {
        given().contentType(ContentType.JSON)
                .body(Map.of("runId", "whatever"))
                .when()
                .put("/api/tests/baselines/NOT_A_TYPE")
                .then()
                .statusCode(400);

        given().contentType(ContentType.JSON)
                .body(Map.of("runId", "no-such-run"))
                .when()
                .put("/api/tests/baselines/LOAD")
                .then()
                .statusCode(404);
    }

    // --------------------------------------------------------------- profiles

    @Test
    void profileCapturesTheMetricsOfTheRunItPointsAt() {
        String runId = seedRun(ItSupport.finishedRun(TestType.LOAD, null, 100_000, 5.0, 42.0));

        // 100_000 records over the fixture's 10s window.
        given().contentType(ContentType.JSON)
                .body(Map.of("name", "golden", "runId", runId))
                .when()
                .post("/api/profiles")
                .then()
                .statusCode(201)
                .body("name", equalTo("golden"))
                .body("runId", equalTo(runId))
                .body("testType", equalTo("LOAD"))
                .body("throughput", equalTo(10_000.0f))
                .body("p99Ms", equalTo(42.0f));

        given().when().get("/api/profiles/golden").then().statusCode(200).body("runId", equalTo(runId));

        given().when()
                .get("/api/profiles")
                .then()
                .statusCode(200)
                .body("count", is(1))
                .body("items.name", hasItem("golden"));

        given().when().delete("/api/profiles/golden").then().statusCode(204);
        given().when().get("/api/profiles/golden").then().statusCode(404);
    }

    @Test
    void profileNamesAreUniqueAndTheFirstWriterWins() {
        String first = seedRun(ItSupport.finishedRun(TestType.LOAD, null, 100_000, 5.0, 20.0));
        String second = seedRun(ItSupport.finishedRun(TestType.STRESS, null, 200_000, 6.0, 25.0));

        given().contentType(ContentType.JSON)
                .body(Map.of("name", "dup", "runId", first))
                .when()
                .post("/api/profiles")
                .then()
                .statusCode(201);

        // The unique constraint on profiles.name only exists in the real
        // schema, so this collision is invisible to the H2 resource tests.
        int status = given().contentType(ContentType.JSON)
                .body(Map.of("name", "dup", "runId", second))
                .when()
                .post("/api/profiles")
                .then()
                .extract()
                .statusCode();
        assertTrue(status >= 400, "a duplicate profile name must be rejected, got " + status);

        // Whatever the status code, the stored profile must not have been
        // silently repointed at the second run.
        given().when().get("/api/profiles/dup").then().statusCode(200).body("runId", equalTo(first));
    }

    // ------------------------------------------------------------------ audit

    @Test
    void deletingARunLeavesAnAuditTrail() {
        String id = seedOne(TestType.LOAD, TestResult.TaskStatus.DONE);

        given().when().delete("/api/tests/" + id).then().statusCode(204);
        given().when().get("/api/tests/" + id).then().statusCode(404);

        // AuditService swallows its own failures, so the only way to know the
        // event was actually written is to read it back through the API.
        Response audit = given().when()
                .get("/api/audit?type=test")
                .then()
                .statusCode(200)
                .body("total", greaterThanOrEqualTo(1))
                .extract()
                .response();

        List<Map<String, Object>> items = audit.jsonPath().getList("items");
        assertTrue(
                items.stream().anyMatch(e -> "DELETE".equals(e.get("action")) && id.equals(e.get("target"))),
                "expected a DELETE audit event targeting " + id + ", got " + items);
        assertEquals("test", items.get(0).get("eventType"));
        assertNotNull(items.get(0).get("timestamp"), "audit events carry a timestamp");
    }

    @Test
    void auditFilteringByTypeIsExactAndUnknownTypesReturnNothing() {
        String id = seedOne(TestType.LOAD, TestResult.TaskStatus.DONE);
        given().when().delete("/api/tests/" + id).then().statusCode(204);

        given().when().get("/api/audit?type=topic").then().statusCode(200).body("total", is(0));

        // An unparseable 'since' is deliberately ignored rather than rejected —
        // pinned so a future "helpful" 400 does not silently break clients.
        given().when().get("/api/audit?since=not-a-timestamp").then().statusCode(200);
    }

    // ----------------------------------------------------------------- fixtures

    private void seed(TestType type, TestResult.TaskStatus status, int count) {
        for (int i = 0; i < count; i++) {
            repository.save(new TestRun(type, ItSupport.singleBrokerSpec(null))
                    .withStatus(status)
                    .withBackend("native"));
        }
    }

    private String seedOne(TestType type, TestResult.TaskStatus status) {
        TestRun run = new TestRun(type, ItSupport.singleBrokerSpec(null))
                .withStatus(status)
                .withBackend("native");
        repository.save(run);
        return run.getId();
    }

    private String seedRun(TestRun run) {
        repository.save(run);
        return run.getId();
    }
}
