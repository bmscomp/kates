package com.bmscomp.kates.resilience;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import java.util.Map;

import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.DisruptionType;
import com.bmscomp.kates.chaos.FaultSpec;

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

        FaultSpec spec = ResilienceScenarios.buildFaultSpec(scenario,
                Map.of("targetPod", "broker-42", "chaosDurationSec", 120));
        assertEquals("broker-42", spec.targetPod());
        assertEquals(120, spec.chaosDurationSec());
    }

    @Test
    void allScenariosHaveUniqueIds() {
        var scenarios = ResilienceScenarios.listAll();
        long uniqueIds = scenarios.stream().map(s -> s.get("id")).distinct().count();
        assertEquals(scenarios.size(), uniqueIds);
    }
}
