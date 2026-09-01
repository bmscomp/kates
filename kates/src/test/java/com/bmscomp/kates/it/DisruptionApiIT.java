package com.bmscomp.kates.it;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.equalTo;
import static org.hamcrest.Matchers.greaterThanOrEqualTo;
import static org.hamcrest.Matchers.hasItem;
import static org.hamcrest.Matchers.hasKey;
import static org.hamcrest.Matchers.hasSize;
import static org.hamcrest.Matchers.is;
import static org.hamcrest.Matchers.notNullValue;

import java.time.Duration;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.restassured.http.ContentType;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.disruption.DisruptionReport;
import com.bmscomp.kates.disruption.DisruptionReportEntity;
import com.bmscomp.kates.disruption.DisruptionReportRepository;
import com.bmscomp.kates.disruption.SlaGrader;

/**
 * The disruption read, analysis and scheduling surfaces against real
 * PostgreSQL.
 *
 * <p>Only {@code GET /api/disruptions/types} had any coverage — five of the six
 * disruption resources had none. The part worth an integration test rather than
 * a unit test is the round trip: a report is stored as a JSON blob in a TEXT
 * column and re-parsed on every read, so the analysis endpoints are only
 * correct if serialisation, storage and deserialisation all agree.
 *
 * <p>Launching disruptions is deliberately out of scope: without a Kubernetes
 * cluster the safety guard finds no broker pods, so a launch resolves to a
 * rejection whose exact shape depends on how the cluster client fails. Reports
 * are therefore seeded directly.
 */
@QuarkusTest
@TestProfile(NoSchedulersTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
class DisruptionApiIT {

    @Inject
    EntityManager em;

    @Inject
    DisruptionReportRepository reports;

    @Inject
    ObjectMapper objectMapper;

    @BeforeEach
    void resetDisruptions() {
        ItSupport.truncate(em, "disruption_reports", "disruption_schedules");
    }

    // ---------------------------------------------------------------- reading

    @Test
    void storedReportSurvivesTheJsonRoundTripThroughPostgres() throws Exception {
        String id = seedReport("az-failure", "COMPLETED", "A", 2, 2, false);

        given().when()
                .get("/api/disruptions/" + id)
                .then()
                .statusCode(200)
                .body("planName", equalTo("az-failure"))
                .body("status", equalTo("COMPLETED"))
                .body("summary.totalSteps", is(2))
                .body("summary.passedSteps", is(2))
                .body("summary.slaViolated", is(false))
                // Durations survive the store-and-reparse round trip at all —
                // the failure mode when Jackson's JavaTimeModule is not wired
                // is a serialisation error, not a wrong value.
                .body("summary.worstRecovery", notNullValue())
                .body("slaVerdict.grade", equalTo("A"))
                .body("summary.peakConsumerLag", is(1500));

        given().when().get("/api/disruptions/no-such-report").then().statusCode(404);
    }

    @Test
    void listingPagesAndFiltersByPlanName() throws Exception {
        seedReport("az-failure", "COMPLETED", "A", 2, 2, false);
        seedReport("az-failure", "PARTIAL", "C", 3, 1, true);
        seedReport("split-brain", "COMPLETED", "B", 1, 1, false);

        given().when()
                .get("/api/disruptions?size=2")
                .then()
                .statusCode(200)
                .body("page", is(0))
                .body("size", is(2))
                .body("count", is(2))
                .body("items[0].id", notNullValue())
                .body("items[0].slaGrade", notNullValue())
                .body("items[0].createdAt", notNullValue());

        given().when()
                .get("/api/disruptions?planName=az-failure")
                .then()
                .statusCode(200)
                .body("count", is(2))
                .body("items.planName", hasItem("az-failure"));
    }

    @Test
    void perStepViewsAreEmptyRatherThanAbsentForAReportWithoutSteps() throws Exception {
        String id = seedReport("az-failure", "COMPLETED", "A", 0, 0, false);

        given().when()
                .get("/api/disruptions/" + id + "/timeline")
                .then()
                .statusCode(200)
                .body("$", hasSize(0));

        given().when()
                .get("/api/disruptions/" + id + "/kafka-metrics")
                .then()
                .statusCode(200)
                .body("$", hasSize(0));

        given().when().get("/api/disruptions/unknown-id/timeline").then().statusCode(404);
        given().when().get("/api/disruptions/unknown-id/kafka-metrics").then().statusCode(404);
    }

    // --------------------------------------------------------------- analysis

    @Test
    void impactScoreIsDerivedFromTheStoredSummary() throws Exception {
        String id = seedReport("az-failure", "COMPLETED", "A", 2, 2, false);

        given().when()
                .get("/api/disruptions/" + id + "/impact")
                .then()
                .statusCode(200)
                .body("overall", notNullValue())
                .body("severity", notNullValue())
                .body("dimensions", hasKey("availability"))
                .body("dimensions", hasKey("latency"))
                .body("dimensions", hasKey("throughput"))
                .body("dimensions", hasKey("replication"))
                .body("dimensions", hasKey("consumerLag"))
                .body("factors", notNullValue());

        given().when().get("/api/disruptions/unknown-id/impact").then().statusCode(404);
    }

    @Test
    void comparisonPlacesTwoRunsSideBySide() throws Exception {
        String baseline = seedReport("az-failure", "COMPLETED", "A", 2, 2, false);
        String current = seedReport("az-failure", "PARTIAL", "C", 2, 1, true);

        given().when()
                .get("/api/disruptions/" + current + "/compare?baselineId=" + baseline)
                .then()
                .statusCode(200)
                .body("currentId", equalTo(current))
                .body("baselineId", equalTo(baseline))
                .body("deltas", hasKey("recoveryDeltaMs"))
                .body("deltas", hasKey("throughputDeltaPercent"))
                .body("deltas", hasKey("p99DeltaPercent"))
                .body("current.slaGrade", equalTo("C"))
                // passedSteps is rendered as "<passed>/<total>", not a number.
                .body("current.passedSteps", equalTo("1/2"))
                .body("baseline.passedSteps", equalTo("2/2"));

        given().when().get("/api/disruptions/" + current + "/compare").then().statusCode(400);
        given().when()
                .get("/api/disruptions/" + current + "/compare?baselineId=nope")
                .then()
                .statusCode(404);
    }

    @Test
    void historyRequiresATopicAndProvidersAreAdvertised() throws Exception {
        seedReport("az-failure", "COMPLETED", "A", 1, 1, false);

        given().when().get("/api/disruptions/history").then().statusCode(400);

        given().when()
                .get("/api/disruptions/history?topic=orders")
                .then()
                .statusCode(200)
                .body("topic", equalTo("orders"))
                .body("count", greaterThanOrEqualTo(0))
                .body("reports", notNullValue());

        given().when().get("/api/disruptions/providers").then().statusCode(200).body("size()", greaterThanOrEqualTo(1));
    }

    // --------------------------------------------------------------- catalogs

    @Test
    void templateAndPlaybookCatalogsAreServedFromTheClasspath() {
        given().when()
                .get("/api/disruptions/templates")
                .then()
                .statusCode(200)
                .body("size()", greaterThanOrEqualTo(1))
                .body("[0].id", notNullValue())
                .body("[0].name", notNullValue())
                .body("[0].category", notNullValue());

        // The six playbook YAMLs are loaded at startup; if one fails to parse
        // the catalog silently shrinks, which nothing would otherwise notice.
        given().when()
                .get("/api/disruptions/playbooks")
                .then()
                .statusCode(200)
                .body("size()", is(6))
                .body("name", hasItem("az-failure"))
                .body("name", hasItem("rolling-restart"))
                .body("[0].steps", greaterThanOrEqualTo(1));

        given().contentType(ContentType.JSON)
                .when()
                .post("/api/disruptions/playbooks/not-a-playbook")
                .then()
                .statusCode(404);
    }

    // -------------------------------------------------------------- schedules

    @Test
    void disruptionScheduleCrudRoundTrip() {
        String id = given().contentType(ContentType.JSON)
                .body(Map.of("name", "nightly-az", "cronExpression", "0 2 * * *", "playbookName", "az-failure"))
                .when()
                .post("/api/disruptions/schedules")
                .then()
                .statusCode(201)
                .body("name", equalTo("nightly-az"))
                .body("cronExpression", equalTo("0 2 * * *"))
                .body("playbookName", equalTo("az-failure"))
                .body("enabled", is(true))
                .body("createdAt", notNullValue())
                .extract()
                .path("id");

        given().when()
                .get("/api/disruptions/schedules")
                .then()
                .statusCode(200)
                .body("count", is(1))
                .body("items[0].name", equalTo("nightly-az"))
                // Null columns are rendered as empty strings rather than null.
                .body("items[0].lastRunId", equalTo(""));

        given().contentType(ContentType.JSON)
                .body(Map.of(
                        "name",
                        "nightly-az",
                        "cronExpression",
                        "30 3 * * *",
                        "playbookName",
                        "az-failure",
                        "enabled",
                        false))
                .when()
                .put("/api/disruptions/schedules/" + id)
                .then()
                .statusCode(200)
                .body("cronExpression", equalTo("30 3 * * *"))
                .body("enabled", is(false));

        given().when().delete("/api/disruptions/schedules/" + id).then().statusCode(204);
        given().when().delete("/api/disruptions/schedules/" + id).then().statusCode(404);
        given().contentType(ContentType.JSON)
                .body(Map.of("name", "gone", "cronExpression", "0 2 * * *", "playbookName", "az-failure"))
                .when()
                .put("/api/disruptions/schedules/" + id)
                .then()
                .statusCode(404);
    }

    @Test
    void scheduleCreationValidatesItsRequiredFields() {
        given().contentType(ContentType.JSON)
                .body(Map.of("cronExpression", "0 2 * * *", "playbookName", "az-failure"))
                .when()
                .post("/api/disruptions/schedules")
                .then()
                .statusCode(400);

        given().contentType(ContentType.JSON)
                .body(Map.of("name", "no-cron", "playbookName", "az-failure"))
                .when()
                .post("/api/disruptions/schedules")
                .then()
                .statusCode(400);

        // Neither a playbook nor an inline plan: there would be nothing to run.
        given().contentType(ContentType.JSON)
                .body(Map.of("name", "empty", "cronExpression", "0 2 * * *"))
                .when()
                .post("/api/disruptions/schedules")
                .then()
                .statusCode(400);
    }

    // ---------------------------------------------------------------- fixture

    private String seedReport(String planName, String status, String grade, int steps, int passed, boolean slaViolated)
            throws Exception {
        DisruptionReport report = new DisruptionReport();
        report.setPlanName(planName);
        report.setStatus(status);
        report.setSummary(new DisruptionReport.DisruptionSummary(
                steps, passed, Duration.ofSeconds(12), 0.15, 0.30, slaViolated, Duration.ofSeconds(20), 1_500L));
        // The grade has to live inside the report JSON as well as in the
        // entity column: the list endpoint reads the column, but /{id}/compare
        // reads the deserialised report's SLA verdict and falls back to "-".
        report.setSlaVerdict(new SlaGrader.SlaVerdict(grade, slaViolated, List.of(), steps, passed));

        String id = UUID.randomUUID().toString();
        reports.save(new DisruptionReportEntity(
                id,
                planName,
                status,
                grade,
                objectMapper.writeValueAsString(report),
                objectMapper.writeValueAsString(report.getSummary())));
        return id;
    }
}
