package com.bmscomp.kates.disruption;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.contains;
import static org.hamcrest.Matchers.equalTo;
import static org.hamcrest.Matchers.is;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.times;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

import java.util.List;
import jakarta.inject.Inject;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import io.restassured.http.ContentType;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;

/**
 * {@code GET /api/disruptions/playbooks/{name}}, the only way for a client to
 * read a playbook's steps: the list returns a step count, and the YAML sits
 * inside the backend jar. Its point is that a client can hand the plan to the
 * dry run and preview a playbook before running it, so the round trip is
 * tested for every shipped playbook.
 */
@QuarkusTest
class DisruptionPlaybookResourceTest {

    @Inject
    DisruptionPlaybookCatalog catalog;

    @Inject
    ObjectMapper objectMapper;

    // The dry run lists broker pods and there is no cluster here, so it would
    // stop at "No broker pods found" before reading a step. The guard is
    // replaced instead, and the plan the dry-run endpoint hands it is captured:
    // that plan, compared with the one the playbook resolves to, is what shows
    // the endpoint's JSON deserialises into the same DisruptionPlan.
    @InjectMock
    DisruptionSafetyGuard safetyGuard;

    @Test
    void returnsTheResolvedPlanOfAShippedPlaybook() {
        given().when()
                .get("/api/disruptions/playbooks/leader-cascade")
                .then()
                .statusCode(200)
                .contentType(ContentType.JSON)
                .body("name", equalTo("playbook:leader-cascade"))
                .body("description", equalTo("Kill partition leaders sequentially to test cascading election recovery"))
                .body("maxAffectedBrokers", is(2))
                .body("autoRollback", is(true))
                .body("isrTrackingTopic", equalTo("__consumer_offsets"))
                .body("steps.name", contains("kill-leader-partition-0", "kill-leader-partition-1"))
                .body("steps[0].faultSpec.disruptionType", equalTo("POD_KILL"))
                .body("steps[0].faultSpec.targetTopic", equalTo("__consumer_offsets"))
                .body("steps[1].faultSpec.targetPartition", is(1))
                .body("steps[0].steadyStateSec", is(30))
                .body("steps[1].steadyStateSec", is(15))
                .body("steps[0].requireRecovery", is(true))
                // Resolved, not echoed: the YAML sets no namespace or selector,
                // and the plan carries the defaults the playbook runs with.
                .body("steps[0].faultSpec.targetNamespace", equalTo("kafka"))
                .body("steps[0].faultSpec.targetLabel", equalTo("strimzi.io/component-type=kafka"));
    }

    @Test
    void unknownPlaybookIsNotFound() {
        given().when()
                .get("/api/disruptions/playbooks/not-a-playbook")
                .then()
                .statusCode(404)
                .contentType(ContentType.JSON)
                .body("status", is(404))
                .body("error", equalTo("Not Found"))
                .body("message", equalTo("Playbook not found: not-a-playbook"));
    }

    @Test
    void everyPlaybookPlanIsAcceptedAsIsByTheDryRun() {
        when(safetyGuard.dryRun(any())).thenAnswer(call -> {
            DisruptionPlan plan = call.getArgument(0);
            var previews = plan.getSteps().stream()
                    .map(s -> new DisruptionSafetyGuard.StepPreview(
                            s.name(), s.faultSpec().disruptionType().name(), null, null, List.of(), List.of()))
                    .toList();
            return new DisruptionSafetyGuard.DryRunResult(true, 3, previews, List.of(), List.of());
        });

        var playbooks = catalog.listAll();
        assertFalse(playbooks.isEmpty(), "no playbook loaded, so nothing would be checked");

        for (var playbook : playbooks) {
            String planJson = given().when()
                    .get("/api/disruptions/playbooks/" + playbook.name)
                    .then()
                    .statusCode(200)
                    .extract()
                    .asString();

            given().contentType(ContentType.JSON)
                    .body(planJson)
                    .when()
                    .post("/api/disruptions?dryRun=true")
                    .then()
                    .statusCode(200)
                    .body("wouldSucceed", is(true))
                    .body(
                            "steps.name",
                            contains(playbook.steps.stream().map(s -> s.name).toArray(String[]::new)));
        }

        ArgumentCaptor<DisruptionPlan> received = ArgumentCaptor.forClass(DisruptionPlan.class);
        verify(safetyGuard, times(playbooks.size())).dryRun(received.capture());
        for (int i = 0; i < playbooks.size(); i++) {
            var playbook = playbooks.get(i);
            assertSamePlan(catalog.toPlan(playbook), received.getAllValues().get(i), playbook.name);
        }
    }

    private void assertSamePlan(DisruptionPlan expected, DisruptionPlan actual, String playbook) {
        assertEquals(expected.getName(), actual.getName(), playbook);
        assertEquals(expected.getDescription(), actual.getDescription(), playbook);
        assertEquals(expected.getMaxAffectedBrokers(), actual.getMaxAffectedBrokers(), playbook);
        assertEquals(expected.isAutoRollback(), actual.isAutoRollback(), playbook);
        assertEquals(expected.getIsrTrackingTopic(), actual.getIsrTrackingTopic(), playbook);
        assertEquals(expected.getLagTrackingGroupId(), actual.getLagTrackingGroupId(), playbook);
        assertEquals(expected.getBaselineDurationSec(), actual.getBaselineDurationSec(), playbook);
        assertEquals(expected.getIsrPollIntervalMs(), actual.getIsrPollIntervalMs(), playbook);
        assertEquals(expected.getLagPollIntervalMs(), actual.getLagPollIntervalMs(), playbook);
        assertEquals(expected.getTestType(), actual.getTestType(), playbook);
        assertEquals(expected.getSla(), actual.getSla(), playbook);
        // Steps and fault specs are records, so this compares every field of
        // every step, the builder defaults the YAML leaves out included.
        assertEquals(expected.getSteps(), actual.getSteps(), playbook);
        // DisruptionPlan is a POJO without equals, and the fields above are
        // listed by hand. As JSON trees, every property the plan serialises is
        // compared, so a field added to DisruptionPlan later that the round
        // trip drops or changes fails here without this test being edited.
        assertEquals(objectMapper.valueToTree(expected), objectMapper.valueToTree(actual), playbook);
    }
}
