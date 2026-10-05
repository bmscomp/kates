package com.bmscomp.kates.resilience;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.*;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;

import com.bmscomp.kates.chaos.DisruptionType;
import com.bmscomp.kates.domain.TestType;

@QuarkusTest
class ResilienceResourceTest {

    /** Stands in for the run, so a scenario that gets through every check injects nothing. */
    @InjectMock
    ResilienceOrchestrator orchestrator;

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
     * A test request with no type is refused before the stream starts. This
     * endpoint runs no bean validation, so the type POST /api/tests requires
     * went unchecked: the run threw as it started, and a scenario's run threw
     * after it had taken a concurrency permit, which it never gave back.
     */
    @Test
    void aTestRequestWithNoTypeIsRefusedByName() {
        given().contentType("application/json")
                .body("{\"testRequest\":{\"spec\":{\"numRecords\":1000}},\"chaosSpec\":{\"experimentName\":\"test\"}}")
                .when()
                .post("/api/resilience")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body("fieldErrors.type", startsWith("the request has no type; set it to one of LOAD, "))
                .body("message", startsWith("type: the request has no type"));

        given().contentType("application/json")
                .body("{\"testRequest\":{\"scenario\":{\"phases\":[{\"name\":\"steady\",\"phaseType\":\"STEADY\"}]}},"
                        + "\"chaosSpec\":{\"experimentName\":\"test\"}}")
                .when()
                .post("/api/resilience")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body("fieldErrors.type", startsWith("neither the request nor its scenario has a type;"))
                .body("message", startsWith("type: neither the request nor its scenario has a type;"));
    }

    /**
     * A null where a scenario phase should be is refused by its index. The
     * checks of the phases read every one, so they threw on it, and the
     * answer was a 500.
     */
    @Test
    void aNullScenarioPhaseIsRefusedByItsIndex() {
        given().contentType("application/json")
                .body("{\"testRequest\":{\"type\":\"LOAD\",\"scenario\":{\"phases\":[null]}},"
                        + "\"chaosSpec\":{\"experimentName\":\"test\"}}")
                .when()
                .post("/api/resilience")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body("fieldErrors", hasKey("phases[0]"))
                .body("message", startsWith("scenario.phases[0]: null is not a phase;"));
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

    /**
     * A scenario brings a fault and its probes, not the load they act on, so
     * a body without a testRequest, or with a null one, is refused.
     */
    @Test
    void runScenarioReturns400WhenNoTestRequestOverride() {
        given().contentType("application/json")
                .body("{}")
                .when()
                .post("/api/resilience/scenarios/broker-crash")
                .then()
                .statusCode(400)
                .body("message", containsString("testRequest"));

        given().contentType("application/json")
                .body("{\"testRequest\":null,\"chaosDurationSec\":30}")
                .when()
                .post("/api/resilience/scenarios/broker-crash")
                .then()
                .statusCode(400)
                .body("error", is("Bad Request"))
                .body("message", startsWith("A 'testRequest' override is required when running a scenario."));
        verify(orchestrator, never()).execute(any());
    }

    /**
     * A scenario runs the test request its body sends, with the scenario's
     * fault and probes, and the fault picks among the brokers. The body's
     * testRequest used to go unread, so every scenario was refused for a
     * missing testRequest. The answer sends a keep-alive byte and looks again
     * 10 s later, so this test takes about 10 s.
     */
    @Test
    void aScenarioRunsTheTestRequestItsBodySends() {
        ResilienceReport report = new ResilienceReport();
        report.setStatus("COMPLETED");
        when(orchestrator.execute(any())).thenReturn(report);

        given().contentType("application/json")
                .body("{\"testRequest\":{\"type\":\"LOAD\",\"spec\":{\"numRecords\":1000,\"throughput\":100}}}")
                .when()
                .post("/api/resilience/scenarios/broker-crash")
                .then()
                .statusCode(200)
                .body("scenario", is("broker-crash"))
                .body("report.status", is("COMPLETED"));

        ArgumentCaptor<ResilienceTestRequest> sent = ArgumentCaptor.forClass(ResilienceTestRequest.class);
        verify(orchestrator).execute(sent.capture());
        ResilienceTestRequest request = sent.getValue();
        assertEquals(TestType.LOAD, request.getTestRequest().getType());
        assertEquals(1000, request.getTestRequest().getSpec().getNumRecords());
        assertEquals(100, request.getTestRequest().getSpec().getThroughput());

        var scenario = ResilienceScenarios.findById("broker-crash");
        assertEquals(DisruptionType.POD_DELETE, request.getChaosSpec().disruptionType());
        assertEquals(ResilienceScenarios.BROKERS, request.getChaosSpec().targetLabel());
        assertEquals(scenario.chaosDurationSec(), request.getChaosSpec().chaosDurationSec());
        assertEquals(scenario.probes(), request.getProbes());
        assertEquals(scenario.steadyStateSec(), request.getSteadyStateSec());
        assertEquals(scenario.maxRecoveryWaitSec(), request.getMaxRecoveryWaitSec());
    }

    /**
     * A scenario's testRequest is refused as POST /api/resilience refuses its
     * own, before the stream starts and before any fault, naming the field.
     */
    @Test
    void aScenarioTestRequestTheRunCannotApplyIsRefusedByName() {
        given().contentType("application/json")
                .body("{\"testRequest\":{\"type\":\"SPIKE\",\"spec\":{\"throughput\":500}}}")
                .when()
                .post("/api/resilience/scenarios/broker-crash")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body("fieldErrors.throughput", containsString("unthrottled"))
                .body("message", containsString("spec.throughput"));
        verify(orchestrator, never()).execute(any());
    }

    /** A testRequest that isn't a test request is a 400 that says why, not a 500. */
    @Test
    void aScenarioTestRequestThatIsNotOneIsRefused() {
        given().contentType("application/json")
                .body("{\"testRequest\":{\"type\":\"BENCHMARK\"}}")
                .when()
                .post("/api/resilience/scenarios/broker-crash")
                .then()
                .statusCode(400)
                .body("error", is("Bad Request"))
                .body("message", containsString("BENCHMARK"));
        verify(orchestrator, never()).execute(any());
    }

    /**
     * A targetLabel that isn't a label selector is refused before the stream
     * starts. The fault would parse it only as it went in, after the
     * benchmark had run for the scenario's steadyStateSec.
     */
    @Test
    void aScenarioTargetLabelThatIsNotASelectorIsRefusedByName() {
        given().contentType("application/json")
                .body("{\"testRequest\":{\"type\":\"LOAD\"},\"targetLabel\":\"zone in (alpha\"}")
                .when()
                .post("/api/resilience/scenarios/broker-crash")
                .then()
                .statusCode(400)
                .body("error", is("Validation Failed"))
                .body("fieldErrors.targetLabel", startsWith("Invalid label selector 'zone in (alpha'"))
                .body("message", startsWith("targetLabel: Invalid label selector 'zone in (alpha'"));
        verify(orchestrator, never()).execute(any());
    }

    /**
     * node-maintenance is listed but not run: a scenario can't name the node
     * to drain, and Litmus node-drain, given none, drains the node of a
     * random pod in any namespace. The answer says where a drain can name it.
     */
    @Test
    void theNodeMaintenanceScenarioIsRefused() {
        given().contentType("application/json")
                .body("{\"testRequest\":{\"type\":\"LOAD\"}}")
                .when()
                .post("/api/resilience/scenarios/node-maintenance")
                .then()
                .statusCode(400)
                .body("error", is("Bad Request"))
                .body("message", startsWith("Scenario node-maintenance is listed but not run"))
                .body("message", endsWith("chaosSpec.envOverrides.TARGET_NODE"));
        verify(orchestrator, never()).execute(any());
    }
}
