package com.bmscomp.kates.resilience;

import static org.junit.jupiter.api.Assertions.*;

import java.util.HashMap;
import java.util.List;
import java.util.Map;

import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.DisruptionType;
import com.bmscomp.kates.chaos.FaultSpec;
import com.bmscomp.kates.chaos.ParsedLabelSelector;

class ResilienceScenariosTest {

    @Test
    void listAllReturnsSevenScenarios() {
        List<Map<String, Object>> scenarios = ResilienceScenarios.listAll();
        assertEquals(7, scenarios.size());
    }

    @Test
    void listAllContainsRequiredFields() {
        List<Map<String, Object>> scenarios = ResilienceScenarios.listAll();
        for (Map<String, Object> s : scenarios) {
            assertNotNull(s.get("id"), "Missing 'id'");
            assertNotNull(s.get("name"), "Missing 'name'");
            assertNotNull(s.get("description"), "Missing 'description'");
            assertNotNull(s.get("disruptionType"), "Missing 'disruptionType'");
            assertNotNull(s.get("probeCount"), "Missing 'probeCount'");
            assertTrue((int) s.get("probeCount") > 0, "probeCount should be > 0");
        }
    }

    @Test
    void findByIdReturnsBrokerCrash() {
        var scenario = ResilienceScenarios.findById("broker-crash");
        assertNotNull(scenario);
        assertEquals("Broker Crash", scenario.name());
        assertEquals(DisruptionType.POD_DELETE, scenario.disruptionType());
        assertEquals(2, scenario.probes().size());
    }

    @Test
    void findByIdReturnsNullForUnknown() {
        assertNull(ResilienceScenarios.findById("nonexistent"));
    }

    @Test
    void buildFaultSpecUsesScenarioDefaults() {
        var scenario = ResilienceScenarios.findById("memory-pressure");
        assertNotNull(scenario);

        FaultSpec spec = ResilienceScenarios.buildFaultSpec(scenario, null);
        assertEquals("memory-pressure", spec.experimentName());
        assertEquals(DisruptionType.MEMORY_STRESS, spec.disruptionType());
        assertEquals(scenario.chaosDurationSec(), spec.chaosDurationSec());
    }

    @Test
    void buildFaultSpecAppliesOverrides() {
        var scenario = ResilienceScenarios.findById("broker-crash");
        assertNotNull(scenario);

        FaultSpec spec =
                ResilienceScenarios.buildFaultSpec(scenario, Map.of("targetPod", "broker-42", "chaosDurationSec", 120));
        assertEquals("broker-42", spec.targetPod());
        assertEquals(120, spec.chaosDurationSec());
    }

    /**
     * Without a targetLabel, a scenario's fault picks among the brokers: a
     * node with both roles is one, a dedicated KRaft controller is not. The
     * builder's default, strimzi.io/component-type=kafka, matched the
     * controllers too, so broker-crash could kill one.
     */
    @Test
    void aScenarioFaultPicksAmongTheBrokers() {
        Map<String, String> broker = strimziPod("true", "false");
        Map<String, String> brokerAndController = strimziPod("true", "true");
        Map<String, String> controller = strimziPod("false", "true");
        for (Map<String, Object> listed : ResilienceScenarios.listAll()) {
            var scenario = ResilienceScenarios.findById((String) listed.get("id"));
            ParsedLabelSelector selector = ParsedLabelSelector.parse(
                    ResilienceScenarios.buildFaultSpec(scenario, null).targetLabel());

            assertTrue(selector.matches(broker), scenario.id());
            assertTrue(selector.matches(brokerAndController), scenario.id());
            assertFalse(selector.matches(controller), scenario.id());
        }
    }

    @Test
    void aTargetLabelOverridePicksOtherPods() {
        var scenario = ResilienceScenarios.findById("broker-crash");
        assertNotNull(scenario);

        FaultSpec spec = ResilienceScenarios.buildFaultSpec(
                scenario, Map.of("targetLabel", "strimzi.io/pool-name=brokers-alpha"));
        assertEquals("strimzi.io/pool-name=brokers-alpha", spec.targetLabel());
    }

    /** A null override is no override. A null targetPod threw, and the answer was a 500. */
    @Test
    void aNullOverrideKeepsTheDefault() {
        var scenario = ResilienceScenarios.findById("broker-crash");
        assertNotNull(scenario);
        Map<String, Object> overrides = new HashMap<>();
        overrides.put("targetLabel", null);
        overrides.put("targetPod", null);

        FaultSpec spec = ResilienceScenarios.buildFaultSpec(scenario, overrides);
        assertEquals(ResilienceScenarios.BROKERS, spec.targetLabel());
        assertEquals("", spec.targetPod());
    }

    /** The labels Strimzi puts on a pod of a KRaft node pool with these roles. */
    private static Map<String, String> strimziPod(String brokerRole, String controllerRole) {
        return Map.of(
                "strimzi.io/component-type", "kafka",
                "strimzi.io/broker-role", brokerRole,
                "strimzi.io/controller-role", controllerRole);
    }

    @Test
    void allScenariosHaveUniqueIds() {
        var scenarios = ResilienceScenarios.listAll();
        long uniqueIds = scenarios.stream().map(s -> s.get("id")).distinct().count();
        assertEquals(scenarios.size(), uniqueIds);
    }
}
