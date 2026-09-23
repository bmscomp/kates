package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.*;

import java.util.Map;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.FaultSpec;
import com.bmscomp.kates.chaos.ParsedLabelSelector;

class DisruptionPlaybookCatalogTest {

    private DisruptionPlaybookCatalog catalog;

    @BeforeEach
    void setup() {
        catalog = new DisruptionPlaybookCatalog();
        catalog.loadPlaybooks();
    }

    @Test
    void azFailureKillsEveryPodTheChartLabelsWithTheZone() {
        FaultSpec spec = catalog.toPlan(catalog.findByName("az-failure").orElseThrow())
                .getSteps()
                .getFirst()
                .faultSpec();

        assertTrue(spec.targetAll(), "an AZ failure takes the whole zone, not one pod of it");
        ParsedLabelSelector selector = ParsedLabelSelector.parse(spec.targetLabel());
        // What charts/kafka-cluster/templates/nodepools.yaml renders for a
        // pool pinned with `zone: alpha`.
        assertTrue(selector.matches(Map.of("strimzi.io/component-type", "kafka", "zone", "alpha")));
        assertFalse(selector.matches(Map.of("strimzi.io/component-type", "kafka", "zone", "sigma")));
        // Node topology labels never reach pods; the old selector relied on them.
        assertFalse(
                selector.matches(Map.of("strimzi.io/component-type", "kafka", "topology.kubernetes.io/zone", "alpha")));
    }

    @Test
    void everyBuiltInSelectorParses() {
        assertEquals(6, catalog.listAll().size());
        for (var entry : catalog.listAll()) {
            for (var step : catalog.toPlan(entry).getSteps()) {
                String label = step.faultSpec().targetLabel();
                assertDoesNotThrow(() -> ParsedLabelSelector.parse(label), entry.name + "/" + step.name());
            }
        }
    }
}
