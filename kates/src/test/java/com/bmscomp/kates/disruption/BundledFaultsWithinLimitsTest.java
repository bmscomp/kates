package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.*;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.FaultLimits;
import com.bmscomp.kates.chaos.FaultSpec;
import com.bmscomp.kates.resilience.ResilienceScenarios;

/**
 * Every fault Kates ships passes the chaos limits as they ship, so a limit
 * tightened below one of them fails here rather than refusing a playbook,
 * template or scenario when someone runs it.
 */
class BundledFaultsWithinLimitsTest {

    private final FaultLimits limits = new FaultLimits();

    /** Each bundled fault outside the default limits, by where it comes from. */
    private final Map<String, Map<String, String>> outside = new LinkedHashMap<>();

    private void check(String where, FaultSpec spec) {
        Map<String, String> found = limits.violations(spec);
        if (!found.isEmpty()) {
            outside.put(where, found);
        }
    }

    private void checkPlan(String where, DisruptionPlan plan) {
        for (DisruptionPlan.DisruptionStep step : plan.getSteps()) {
            check(where + "/" + step.name(), step.faultSpec());
        }
    }

    @Test
    void everyPlaybookTemplateAndScenarioIsWithinTheDefaultLimits() {
        DisruptionPlaybookCatalog playbooks = new DisruptionPlaybookCatalog();
        playbooks.loadPlaybooks();
        List<DisruptionPlaybookCatalog.PlaybookEntry> entries = playbooks.listAll();
        // Not vacuous: every playbook file loaded.
        assertEquals(6, entries.size());
        entries.forEach(entry -> checkPlan("playbook " + entry.name, playbooks.toPlan(entry)));

        ChaosTemplateCatalog templates = new ChaosTemplateCatalog();
        List<ChaosTemplateCatalog.TemplateInfo> templateInfos = templates.listTemplates();
        assertEquals(8, templateInfos.size());
        templateInfos.forEach(t -> checkPlan("template " + t.id(), templates.buildPlan(t.id())));

        List<Map<String, Object>> scenarios = ResilienceScenarios.listAll();
        assertEquals(7, scenarios.size());
        scenarios.forEach(s -> {
            String id = (String) s.get("id");
            check("scenario " + id, ResilienceScenarios.buildFaultSpec(ResilienceScenarios.findById(id), null));
        });

        assertEquals(Map.of(), outside);
    }

    /**
     * An override is checked, not clamped. One past the int range used to
     * wrap: 4294967356 seconds came out as 60, and the template ran.
     */
    @Test
    void anOverridePastTheIntRangeIsRefusedNotWrappedIntoRange() {
        long wrapsTo60 = (1L << 32) + 60;

        FaultSpec template = new ChaosTemplateCatalog()
                .buildPlan("network-partition-split-brain", Map.of("chaosDurationSec", wrapsTo60))
                .getSteps()
                .getFirst()
                .faultSpec();
        FaultSpec scenario = ResilienceScenarios.buildFaultSpec(
                ResilienceScenarios.findById("network-split"), Map.of("chaosDurationSec", wrapsTo60));

        for (FaultSpec spec : List.of(template, scenario)) {
            assertEquals(Integer.MAX_VALUE, spec.chaosDurationSec());
            assertEquals(
                    Map.of(
                            "chaosDurationSec",
                            Integer.MAX_VALUE + " is above the limit of 3600 (kates.chaos.limits.max-duration-sec)"),
                    limits.violations(spec));
        }
    }
}
