package com.bmscomp.kates.resilience;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.*;

import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.Test;

@QuarkusTest
class ResilienceResourceTest {

    @Test
    void postResilienceReturnsBadRequestWithoutTestRequest() {
        given().contentType("application/json")
                .body("{\"chaosSpec\":{\"experimentName\":\"test\"}}")
                .when()
                .post("/api/resilience")
                .then()
                .statusCode(400)
                .body("message", containsString("testRequest"));
    }

    @Test
    void postResilienceReturnsBadRequestWithoutChaosSpec() {
        given().contentType("application/json")
                .body("{\"testRequest\":{\"testType\":\"LOAD\"}}")
                .when()
                .post("/api/resilience")
                .then()
                .statusCode(400)
                .body("message", containsString("chaosSpec"));
    }

    /**
     * A test request the backend would refuse is refused before the stream
     * starts, naming the field. It used to go into the resilience run, which
     * ended ERROR with the reason only in the server log.
     */
    @Test
    void aFieldTheRunCannotApplyIsRefusedByName() {
        given().contentType("application/json")
                .body("{\"testRequest\":{\"type\":\"SPIKE\",\"spec\":{\"throughput\":500}},"
                        + "\"chaosSpec\":{\"experimentName\":\"test\"}}")
                .when()
                .post("/api/resilience")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body("fieldErrors.throughput", containsString("unthrottled"))
                .body("message", containsString("spec.throughput"));
    }

    /**
     * A resilience run never went through the safety guard, so its fault went
     * in with any parameter. The coordinator refuses it now, but only after
     * the benchmark has run, so it is refused here, before the stream starts.
     */
    @Test
    void aChaosSpecOutsideTheChaosLimitsIsRefusedByName() {
        given().contentType("application/json")
                .body("{\"testRequest\":{\"type\":\"LOAD\"},\"chaosSpec\":{\"experimentName\":\"split\","
                        + "\"disruptionType\":\"NETWORK_PARTITION\",\"chaosDurationSec\":0}}")
                .when()
                .post("/api/resilience")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body(
                        "fieldErrors.chaosDurationSec",
                        is("0 is below 1, and a NETWORK_PARTITION is undone only when its duration ends"))
                .body("message", startsWith("chaosSpec.chaosDurationSec: 0 is below 1"));
    }

    @Test
    void aScenarioOverrideOutsideTheChaosLimitsIsRefusedByName() {
        given().contentType("application/json")
                .body("{\"chaosDurationSec\":86400}")
                .when()
                .post("/api/resilience/scenarios/network-split")
                .then()
                .statusCode(400)
                .body("fieldErrors.chaosDurationSec", containsString("kates.chaos.limits.max-duration-sec"));
    }

    @Test
    void listScenariosReturnsSevenEntries() {
        given().when().get("/api/resilience/scenarios").then().statusCode(200).body("$.size()", is(7));
    }

    @Test
    void listScenariosContainsExpectedFields() {
        given().when()
                .get("/api/resilience/scenarios")
                .then()
                .statusCode(200)
                .body("[0].id", notNullValue())
                .body("[0].name", notNullValue())
                .body("[0].description", notNullValue())
                .body("[0].disruptionType", notNullValue())
                .body("[0].probeCount", greaterThan(0));
    }

    @Test
    void runScenarioReturns404ForUnknownId() {
        given().contentType("application/json")
                .body("{}")
                .when()
                .post("/api/resilience/scenarios/nonexistent")
                .then()
                .statusCode(404)
                .body("message", containsString("nonexistent"));
    }

    @Test
    void runScenarioReturns400WhenNoTestRequestOverride() {
        given().contentType("application/json")
                .body("{}")
                .when()
                .post("/api/resilience/scenarios/broker-crash")
                .then()
                .statusCode(400)
                .body("message", containsString("testRequest"));
    }
}
