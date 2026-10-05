package com.bmscomp.kates.resilience;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.*;
import static org.junit.jupiter.api.Assertions.assertEquals;

import java.util.Map;
import java.util.Set;

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
     * A scenario phase without a phaseType is refused before the stream
     * starts, naming the phase; the fields it sends that a phase does not have
     * are ignored. Its run used to fail as it started, and the resilience run
     * went on to inject its fault against no load.
     */
    @Test
    void aScenarioPhaseWithoutAPhaseTypeIsRefusedByName() {
        given().contentType("application/json")
                .body("{\"testRequest\":{\"type\":\"LOAD\",\"scenario\":{\"phases\":[{\"name\":\"events-load\","
                        + "\"topic\":\"kates-events\",\"throughput\":2000}]}},"
                        + "\"chaosSpec\":{\"experimentName\":\"test\"}}")
                .when()
                .post("/api/resilience")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body("fieldErrors", hasKey("phases[0].phaseType"))
                .body("message", startsWith("scenario.phases[0].phaseType: phase events-load has no phaseType"));
    }

    /**
     * A spec value outside the limits POST /api/tests holds a spec to is
     * refused before the stream starts, naming the field. This endpoint runs
     * no bean validation, so such a value used to go into the run: a
     * numRecords of 0 sent nothing, a topic Kafka cannot create failed the
     * run, and the fault went in all the same.
     */
    @Test
    void aSpecValueOutsideItsLimitsIsRefusedByName() {
        given().contentType("application/json")
                .body("{\"testRequest\":{\"type\":\"LOAD\",\"spec\":{\"numRecords\":0,\"topic\":\"not a topic!\"}},"
                        + "\"chaosSpec\":{\"experimentName\":\"test\"}}")
                .when()
                .post("/api/resilience")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body("fieldErrors.numRecords", notNullValue())
                .body("fieldErrors.topic", is("topic must be a legal Kafka topic name"))
                .body("message", startsWith("spec.numRecords: "))
                .body("message", endsWith("; spec.topic: topic must be a legal Kafka topic name"));
    }

    /**
     * The limits are those of POST /api/tests, and so is the fieldErrors that
     * names a value outside them. As there, they are checked before the
     * fields the run could not apply, here the rate SPIKE ignores.
     */
    @Test
    void theLimitsAndTheirFieldErrorsAreThoseOfPostApiTests() {
        String testRequest = "{\"type\":\"SPIKE\",\"spec\":{\"numRecords\":0,\"topic\":\"not a topic!\","
                + "\"acks\":\"2\",\"throughput\":500}}";
        Map<String, String> tests = given().contentType("application/json")
                .body(testRequest)
                .when()
                .post("/api/tests")
                .then()
                .statusCode(400)
                .extract()
                .path("fieldErrors");
        Map<String, String> resilience = given().contentType("application/json")
                .body("{\"testRequest\":" + testRequest + ",\"chaosSpec\":{\"experimentName\":\"test\"}}")
                .when()
                .post("/api/resilience")
                .then()
                .statusCode(400)
                .extract()
                .path("fieldErrors");

        assertEquals(Set.of("numRecords", "topic", "acks"), tests.keySet());
        assertEquals(tests, resilience);
    }

    /**
     * A scenario's base spec and phase specs are held to the same limits, each
     * value keyed by its path in the scenario, as the fields its phases could
     * not apply are. Only the scenario has a type, and the check asks for no
     * other: bean validation of the whole testRequest would have required the
     * request's own.
     */
    @Test
    void aScenarioSpecValueOutsideItsLimitsIsRefusedByItsPath() {
        given().contentType("application/json")
                .body("{\"testRequest\":{\"scenario\":{\"type\":\"LOAD\",\"baseSpec\":{\"acks\":\"2\"},\"phases\":["
                        + "{\"name\":\"steady\",\"phaseType\":\"STEADY\",\"spec\":{\"numRecords\":0}}]}},"
                        + "\"chaosSpec\":{\"experimentName\":\"test\"}}")
                .when()
                .post("/api/resilience")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body("fieldErrors", aMapWithSize(2))
                .body("fieldErrors.'baseSpec.acks'", is("acks must be one of: all, -1, 0, 1"))
                .body("fieldErrors", hasKey("phases[0].spec.numRecords"))
                .body(
                        "message",
                        startsWith("scenario.baseSpec.acks: acks must be one of: all, -1, 0, 1;"
                                + " scenario.phases[0].spec.numRecords: "));
    }

    /** A null phase has no spec to check, and the specs after it are still checked. */
    @Test
    void theSpecsAfterANullPhaseAreChecked() {
        given().contentType("application/json")
                .body("{\"testRequest\":{\"type\":\"LOAD\",\"scenario\":{\"phases\":[null,"
                        + "{\"name\":\"steady\",\"phaseType\":\"STEADY\",\"spec\":{\"topic\":\"not a topic!\"}}]}},"
                        + "\"chaosSpec\":{\"experimentName\":\"test\"}}")
                .when()
                .post("/api/resilience")
                .then()
                .statusCode(400)
                .body("fieldErrors", aMapWithSize(1))
                .body("fieldErrors.'phases[1].spec.topic'", is("topic must be a legal Kafka topic name"));
    }

    /**
     * A scenario with a type of its own needs none on the request, and with
     * its specs within their limits, this one passes them and is refused
     * only for its fault.
     */
    @Test
    void aScenarioWithItsOwnTypeNeedsNoRequestType() {
        given().contentType("application/json")
                .body("{\"testRequest\":{\"scenario\":{\"type\":\"LOAD\",\"baseSpec\":{\"numRecords\":1000},"
                        + "\"phases\":[{\"name\":\"steady\",\"phaseType\":\"STEADY\","
                        + "\"spec\":{\"topic\":\"kates-events\"}}]}},"
                        + "\"chaosSpec\":{\"experimentName\":\"split\",\"disruptionType\":\"NETWORK_PARTITION\","
                        + "\"chaosDurationSec\":0}}")
                .when()
                .post("/api/resilience")
                .then()
                .statusCode(400)
                .body("fieldErrors", aMapWithSize(1))
                .body("fieldErrors", hasKey("chaosDurationSec"));
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
