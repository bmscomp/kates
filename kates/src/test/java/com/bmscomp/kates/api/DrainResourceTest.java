package com.bmscomp.kates.api;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.*;

import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;

@QuarkusTest
class DrainResourceTest {

    private static final String PLAN = "{\"name\":\"forever\",\"steps\":[{\"name\":\"split\",\"faultSpec\":"
            + "{\"experimentName\":\"split\",\"disruptionType\":\"NETWORK_PARTITION\",\"chaosDurationSec\":0}}]}";

    @AfterEach
    void lift() {
        drain(false);
    }

    private static void drain(boolean draining) {
        given().contentType("application/json")
                .body("{\"draining\": " + draining + "}")
                .when()
                .put("/api/drain")
                .then()
                .statusCode(200)
                .body("draining", is(draining));
    }

    @Test
    void theApiIsNotDrainingUntilAsked() {
        given().when()
                .get("/api/drain")
                .then()
                .statusCode(200)
                .body("draining", is(false))
                .body("drained", is(false));
        given().when().get("/api/health").then().body("capabilities", hasItem("drain"));
    }

    @Test
    void aDrainWithNothingLiveIsDrained() {
        drain(true);

        given().when()
                .get("/api/drain")
                .then()
                .statusCode(200)
                .body("draining", is(true))
                .body("liveRuns", is(0))
                .body("faultsInFlight", is(0))
                .body("drained", is(true));
    }

    @Test
    void aBodyWithoutTheFieldIsRefused() {
        given().contentType("application/json")
                .body("{}")
                .when()
                .put("/api/drain")
                .then()
                .statusCode(400)
                .body("message", is("draining must be true or false"));
        given().when().get("/api/drain").then().body("draining", is(false));
    }

    @Test
    void aDrainingApiStartsNoRunAndTakesNoPermit() {
        drain(true);

        given().contentType("application/json")
                .body("{\"type\": \"ROUND_TRIP\", \"spec\": {}}")
                .when()
                .post("/api/tests")
                .then()
                .statusCode(503)
                .header("Retry-After", "60")
                .body("error", is("Service Unavailable"))
                .body("message", is("Kates is draining: no new run is started until the drain is lifted"));
        given().when().get("/api/drain").then().body("liveRuns", is(0)).body("drained", is(true));
    }

    /** The same plan is answered 422 once the drain is lifted, so the 503 came first. */
    @Test
    void aDrainingApiStartsNoFaultAndCountsNone() {
        drain(true);

        given().contentType("application/json")
                .body(PLAN)
                .when()
                .post("/api/disruptions")
                .then()
                .statusCode(503)
                .header("Retry-After", "60")
                .body("message", is("Kates is draining: no new fault is started until the drain is lifted"));
        given().contentType("application/json")
                .body("{}")
                .when()
                .post("/api/disruptions/templates/no-such-template")
                .then()
                .statusCode(503);
        given().when().get("/api/drain").then().body("faultsInFlight", is(0));

        drain(false);
        given().contentType("application/json")
                .body(PLAN)
                .when()
                .post("/api/disruptions")
                .then()
                .statusCode(422);
        given().contentType("application/json")
                .body("{}")
                .when()
                .post("/api/disruptions/templates/no-such-template")
                .then()
                .statusCode(404);
        given().when().get("/api/drain").then().body("faultsInFlight", is(0));
    }

    /** A dry run injects nothing, so a drain does not refuse it. */
    @Test
    void aDryRunIsStillAnswered() {
        drain(true);

        given().contentType("application/json")
                .body(PLAN)
                .when()
                .post("/api/disruptions?dryRun=true")
                .then()
                .statusCode(not(503));
    }
}
