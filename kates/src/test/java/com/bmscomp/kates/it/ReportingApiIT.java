package com.bmscomp.kates.it;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.containsString;
import static org.hamcrest.Matchers.equalTo;
import static org.hamcrest.Matchers.greaterThanOrEqualTo;
import static org.hamcrest.Matchers.hasItem;
import static org.hamcrest.Matchers.hasKey;
import static org.hamcrest.Matchers.hasSize;
import static org.hamcrest.Matchers.instanceOf;
import static org.hamcrest.Matchers.is;
import static org.hamcrest.Matchers.notNullValue;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.List;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;

import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.TestRunRepository;

/**
 * Report and trend generation over runs that actually round-tripped through
 * PostgreSQL.
 *
 * <p>{@code ReportResourceTest} mocks the repository and the generator, so it
 * only ever asserts {@code status >= 400} for a missing run — the happy path of
 * every exporter is untested, and so is the whole trend surface below the
 * service boundary. The interesting failure mode these cover is the one a mock
 * hides: results are stored as child rows and re-hydrated through
 * {@code EntityMapper}, so a report is only correct if that projection is.
 */
@QuarkusTest
@TestProfile(NoSchedulersTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
class ReportingApiIT {

    @Inject
    EntityManager em;

    @Inject
    TestRunRepository repository;

    private String runId;

    @BeforeEach
    void seedHistory() {
        // Fixtures deliberately carry no topic: ReportGenerator captures a
        // cluster snapshot for any run whose spec names one, and with no broker
        // attached that costs a 15-second AdminClient timeout per run.
        ItSupport.truncate(em, "test_runs", "outbox_events", "audit_events", "baseline_runs");
        runId = save(ItSupport.finishedRun(TestType.LOAD, null, 100_000, 5.0, 40.0));
    }

    @Test
    void fullReportIsBuiltFromThePersistedResultRows() {
        given().when()
                .get("/api/tests/" + runId + "/report")
                .then()
                .statusCode(200)
                .body("run.id", equalTo(runId))
                .body("run.testType", equalTo("LOAD"))
                .body("summary.totalRecords", is(100_000))
                .body("summary.p99LatencyMs", equalTo(40.0f))
                .body("generatedAt", notNullValue());

        given().when()
                .get("/api/tests/" + runId + "/report/summary")
                .then()
                .statusCode(200)
                .body("totalRecords", is(100_000))
                .body("avgThroughputRecPerSec", equalTo(10_000.0f))
                .body("p50LatencyMs", equalTo(5.0f));
    }

    @Test
    void everyExporterRendersTheSameRun() {
        given().when()
                .get("/api/tests/" + runId + "/report/markdown")
                .then()
                .statusCode(200)
                .header("Content-Type", containsString("text/markdown"))
                .body(containsString(runId));

        // The download endpoints are only useful if the browser is told to save
        // the file, so the disposition header is part of the contract.
        given().when()
                .get("/api/tests/" + runId + "/report/csv")
                .then()
                .statusCode(200)
                .header("Content-Type", containsString("text/csv"))
                .header("Content-Disposition", containsString("kates-report-" + runId + ".csv"))
                .body(containsString("totalRecords"));

        given().when()
                .get("/api/tests/" + runId + "/report/junit")
                .then()
                .statusCode(200)
                .header("Content-Disposition", containsString("kates-report-" + runId + ".xml"))
                .body(containsString("<?xml"))
                .body(containsString("testsuite"));
    }

    @Test
    void reportsWithoutCapturedInfrastructureDataSayNoRatherThanEmpty() {
        // These three only have data when a run executed against a live
        // cluster. A seeded run has none, and the distinction between "no data"
        // and "empty list" is the actual behaviour worth pinning: brokers
        // answers 200 with [], the other two answer 404.
        given().when()
                .get("/api/tests/" + runId + "/report/brokers")
                .then()
                .statusCode(200)
                .body("$", hasSize(0));

        given().when()
                .get("/api/tests/" + runId + "/report/snapshot")
                .then()
                .statusCode(404)
                .body(containsString(runId));

        given().when().get("/api/tests/" + runId + "/report/heatmap").then().statusCode(404);
    }

    @Test
    void comparisonReportComputesDeltasBetweenTwoStoredRuns() {
        String slower = save(ItSupport.finishedRun(TestType.LOAD, null, 50_000, 9.0, 80.0));

        given().when()
                .get("/api/tests/reports/compare?ids=" + runId + "," + slower)
                .then()
                .statusCode(200)
                .body("baselineRunId", equalTo(runId))
                .body("runs", hasSize(2))
                .body("deltas", hasKey("throughputRecPerSec"))
                .body("deltas", hasKey("p99LatencyMs"))
                .body("deltas", hasKey("totalRecords"));

        given().when().get("/api/tests/reports/compare?ids=" + runId).then().statusCode(400);
        given().when().get("/api/tests/reports/compare").then().statusCode(400);
    }

    @Test
    void advisorAnalysesAPersistedRun() {
        // Which recommendations fire depends on the spec and on cluster
        // reachability; that a persisted run can be analysed at all, and that
        // an unknown one is a clean 404, is what has no coverage.
        given().when()
                .get("/api/tests/" + runId + "/advisor")
                .then()
                .statusCode(200)
                .body("$", instanceOf(List.class));

        given().when().get("/api/tests/no-such-run/advisor").then().statusCode(404);
    }

    @Test
    void tuningCatalogIsServedIndependentlyOfAnyRun() {
        given().when().get("/api/tests/tuning/types").then().statusCode(200).body("size()", greaterThanOrEqualTo(1));

        // A run that was never a tuning sweep has no tuning report.
        given().when().get("/api/tests/" + runId + "/report/tuning").then().statusCode(404);
    }

    @Test
    void trendsAreDerivedFromTheRunsInTheWindow() {
        save(ItSupport.finishedRun(TestType.LOAD, null, 90_000, 5.5, 45.0));
        save(ItSupport.finishedRun(TestType.LOAD, null, 80_000, 6.0, 50.0));

        // Three LOAD runs exist inside the 30-day window; the trend must find
        // all of them, which is only true if the date-range query and the
        // summary projection agree.
        given().when()
                .get("/api/trends?type=LOAD&days=30")
                .then()
                .statusCode(200)
                .body("testType", equalTo("LOAD"))
                .body("metric", equalTo("avgThroughputRecPerSec"))
                .body("dataPoints", hasSize(3))
                .body("dataPoints.runId", hasItem(runId));

        given().when()
                .get("/api/trends?type=LOAD&days=30&metric=p99LatencyMs")
                .then()
                .statusCode(200)
                .body("metric", equalTo("p99LatencyMs"))
                .body("dataPoints", hasSize(3));

        // A type with no history is an empty trend, not an error.
        given().when()
                .get("/api/trends?type=ENDURANCE&days=30")
                .then()
                .statusCode(200)
                .body("dataPoints", hasSize(0));
    }

    @Test
    void trendSubResourcesShareTheSameValidation() {
        given().when().get("/api/trends/phases?type=LOAD&days=30").then().statusCode(200);
        given().when().get("/api/trends/breakdown?type=LOAD&days=30").then().statusCode(200);
        given().when()
                .get("/api/trends/broker?type=LOAD&days=30&brokerId=0")
                .then()
                .statusCode(200)
                .body("brokerId", is(0));

        given().when().get("/api/trends").then().statusCode(400);
        given().when().get("/api/trends?type=NOPE").then().statusCode(400);
        given().when().get("/api/trends?type=LOAD&days=0").then().statusCode(400);
        given().when().get("/api/trends?type=LOAD&days=9999").then().statusCode(400);
    }

    @Test
    void reportEndpointsRejectUnknownRuns() {
        int status = given().when()
                .get("/api/tests/no-such-run/report")
                .then()
                .extract()
                .statusCode();
        assertTrue(status >= 400, "a report for a missing run must not be a 2xx, got " + status);

        given().when().get("/api/tests/no-such-run/report/regression").then().statusCode(404);
    }

    private String save(TestRun run) {
        repository.save(run);
        return run.getId();
    }
}
