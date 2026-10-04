package com.bmscomp.kates.disruption;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.*;

import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.Test;

@QuarkusTest
class DisruptionResourceTest {

    @Test
    void listTypesReturnsAllDisruptionTypes() {
        given().when().get("/api/disruptions/types").then().statusCode(200).body("$.size()", is(13));
    }

    @Test
    void listTypesContainsNewExperimentTypes() {
        given().when()
                .get("/api/disruptions/types")
                .then()
                .statusCode(200)
                .body("find { it.name == 'MEMORY_STRESS' }.description", notNullValue())
                .body("find { it.name == 'IO_STRESS' }.description", notNullValue())
                .body("find { it.name == 'DNS_ERROR' }.description", notNullValue());
    }

    @Test
    void listTypesContainsTypeAndDescription() {
        given().when()
                .get("/api/disruptions/types")
                .then()
                .statusCode(200)
                .body("[0].name", notNullValue())
                .body("[0].description", notNullValue());
    }

    /** Said whatever the cluster holds: here there is none, and no broker pod either. */
    @Test
    void aPlanWithAFaultOutsideTheChaosLimitsIsRejectedNamingItsStep() {
        given().contentType("application/json")
                .body(
                        "{\"name\":\"forever\",\"steps\":[{\"name\":\"split\",\"faultSpec\":{\"experimentName\":\"split\","
                                + "\"disruptionType\":\"NETWORK_PARTITION\",\"chaosDurationSec\":0}}]}")
                .when()
                .post("/api/disruptions")
                .then()
                .statusCode(422)
                .body("status", is("REJECTED"))
                .body(
                        "validationWarnings",
                        hasItem("ERROR: Step 'split': chaosDurationSec 0 is below 1, and a NETWORK_PARTITION is undone"
                                + " only when its duration ends"));
    }

    /**
     * The compound endpoint calls the chaos providers itself, past the safety
     * guard, and its faults went in with any parameter. Now none goes in
     * while one is outside the chaos limits, the pod kill listed first included.
     */
    @Test
    void aCompoundRunWithAFaultOutsideTheChaosLimitsIsRefused() {
        given().contentType("application/json")
                .body("{\"faults\":[{\"faultSpec\":{\"experimentName\":\"kill\",\"disruptionType\":\"POD_KILL\"}},"
                        + "{\"faultSpec\":{\"experimentName\":\"split\",\"disruptionType\":\"NETWORK_PARTITION\","
                        + "\"chaosDurationSec\":0}}]}")
                .when()
                .post("/api/disruptions/compound")
                .then()
                .statusCode(400)
                .body(
                        "message",
                        startsWith("No fault was triggered: faults[1].faultSpec.chaosDurationSec 0 is below 1"));
    }

    @Test
    void executeDisruptionReturns400WithEmptyPlan() {
        given().contentType("application/json")
                .body("{}")
                .when()
                .post("/api/disruptions")
                .then()
                .statusCode(400);
    }
}
