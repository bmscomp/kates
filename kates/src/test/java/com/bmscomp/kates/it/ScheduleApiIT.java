package com.bmscomp.kates.it;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.equalTo;
import static org.hamcrest.Matchers.hasItem;
import static org.hamcrest.Matchers.hasKey;
import static org.hamcrest.Matchers.hasSize;
import static org.hamcrest.Matchers.is;
import static org.hamcrest.Matchers.notNullValue;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.List;
import java.util.Map;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;

import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.restassured.http.ContentType;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.schedule.ScheduledTestRunRepository;

/**
 * The schedules API against real PostgreSQL.
 *
 * <p>{@code ScheduleResourceTest} runs on H2 with Hibernate generating the
 * DDL from the entity, so it never sees the Flyway table the scheduler reads in
 * production: its column widths, its defaults, or what a value the API accepts
 * but the column cannot hold does to the request.
 *
 * <p>Every schedule here is created disabled: {@code TestScheduler} evaluates
 * enabled schedules every minute, and a firing would start a run with no broker
 * attached.
 */
@QuarkusTest
@TestProfile(NoSchedulersTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
class ScheduleApiIT {

    private static final String LOAD_REQUEST =
            "{\"type\":\"LOAD\",\"backend\":\"native\",\"spec\":{\"numRecords\":1000}}";

    @Inject
    EntityManager em;

    @Inject
    ScheduledTestRunRepository repository;

    @BeforeEach
    void resetSchedules() {
        ItSupport.truncate(em, "scheduled_test_runs", "audit_events");
    }

    @Test
    void scheduleRoundTripsThroughTheRealTable() {
        String id = create("nightly", "0 2 * * *");

        given().when()
                .get("/api/schedules/" + id)
                .then()
                .statusCode(200)
                .body("name", equalTo("nightly"))
                .body("cronExpression", equalTo("0 2 * * *"))
                .body("enabled", is(false))
                .body("createdAt", notNullValue());

        given().when()
                .get("/api/schedules")
                .then()
                .statusCode(200)
                .body("$", hasSize(1))
                .body("id", hasItem(id));

        // The scheduler fires requestJson as stored, so what went in has to be
        // what comes back out of the TEXT column.
        String stored = repository.findById(id).orElseThrow().getRequestJson();
        assertTrue(stored.contains("\"numRecords\":1000"), "stored request lost its spec: " + stored);

        given().when().delete("/api/schedules/" + id).then().statusCode(204);
        given().when().get("/api/schedules/" + id).then().statusCode(404);
        given().when().delete("/api/schedules/" + id).then().statusCode(404);
    }

    @Test
    void renamingKeepsTheTestRequestAndTheEnabledFlag() {
        String id = create("nightly", "0 2 * * *");
        String before = repository.findById(id).orElseThrow().getRequestJson();

        given().contentType(ContentType.JSON)
                .body(Map.of("name", "renamed"))
                .when()
                .put("/api/schedules/" + id)
                .then()
                .statusCode(200)
                .body("name", equalTo("renamed"))
                .body("cronExpression", equalTo("0 2 * * *"));

        // A rename that leaves enabled out must not switch a disabled schedule
        // on: it would start firing at its next matching minute.
        given().when().get("/api/schedules/" + id).then().statusCode(200).body("enabled", is(false));
        assertEquals(before, repository.findById(id).orElseThrow().getRequestJson());
    }

    @Test
    void refusedRequestsSaveNothing() {
        given().contentType(ContentType.JSON)
                .body("{\"name\":\"nightly\",\"cronExpression\":\"0 2 * * *\",\"enabled\":false,"
                        + "\"testRequest\":{\"type\":\"LOAD\",\"backend\":\"no-such-backend\"}}")
                .when()
                .post("/api/schedules")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body("fieldErrors", hasKey("testRequest.backend"));

        given().when().get("/api/schedules").then().statusCode(200).body("$", hasSize(0));
    }

    @Test
    void valuesWiderThanTheirColumnsAreRefusedNotFailed() {
        // name is VARCHAR(128) and cron_expression VARCHAR(64) in V2; a longer
        // value the API let through reached Postgres and came back as a 500.
        String longName = "n".repeat(129);
        String longCron = "0 2 * * *" + " ".repeat(60);

        given().contentType(ContentType.JSON)
                .body(Map.of(
                        "name",
                        longName,
                        "cronExpression",
                        "0 2 * * *",
                        "enabled",
                        false,
                        "testRequest",
                        loadRequest()))
                .when()
                .post("/api/schedules")
                .then()
                .statusCode(400);

        given().contentType(ContentType.JSON)
                .body(Map.of(
                        "name", "nightly", "cronExpression", longCron, "enabled", false, "testRequest", loadRequest()))
                .when()
                .post("/api/schedules")
                .then()
                .statusCode(400);

        given().when().get("/api/schedules").then().statusCode(200).body("$", hasSize(0));

        // PUT skips @Valid, so it has to hold the same widths itself.
        String id = create("nightly", "0 2 * * *");
        given().contentType(ContentType.JSON)
                .body(Map.of("name", longName))
                .when()
                .put("/api/schedules/" + id)
                .then()
                .statusCode(400);
        given().contentType(ContentType.JSON)
                .body(Map.of("cronExpression", longCron))
                .when()
                .put("/api/schedules/" + id)
                .then()
                .statusCode(400);
        given().when().get("/api/schedules/" + id).then().statusCode(200).body("name", equalTo("nightly"));
    }

    @Test
    void createAndDeleteLeaveAnAuditTrail() {
        String id = create("nightly", "0 2 * * *");
        given().when().delete("/api/schedules/" + id).then().statusCode(204);

        List<Map<String, Object>> items = given().when()
                .get("/api/audit?type=schedule")
                .then()
                .statusCode(200)
                .extract()
                .jsonPath()
                .getList("items");

        List<Object> actions = items.stream().map(e -> e.get("action")).toList();
        assertTrue(actions.containsAll(List.of("CREATE", "DELETE")), "expected CREATE and DELETE, got " + items);
    }

    private String create(String name, String cron) {
        return given().contentType(ContentType.JSON)
                .body("{\"name\":\"" + name + "\",\"cronExpression\":\"" + cron + "\",\"enabled\":false,"
                        + "\"testRequest\":" + LOAD_REQUEST + "}")
                .when()
                .post("/api/schedules")
                .then()
                .statusCode(201)
                .extract()
                .path("id");
    }

    private static Map<String, Object> loadRequest() {
        return Map.of("type", "LOAD", "backend", "native");
    }
}
