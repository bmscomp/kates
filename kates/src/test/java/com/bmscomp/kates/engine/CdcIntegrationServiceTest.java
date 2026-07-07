package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.regex.Pattern;

import org.junit.jupiter.api.Test;

class CdcIntegrationServiceTest {

    @Test
    void createRunResourcesUsesDistinctNamesAcrossRuns() {
        CdcIntegrationService.CdcRunResources first = CdcIntegrationService.createRunResources("run-alpha");
        CdcIntegrationService.CdcRunResources second = CdcIntegrationService.createRunResources("run-beta");

        assertNotEquals(first.sourceConnectorName(), second.sourceConnectorName());
        assertNotEquals(first.sinkConnectorName(), second.sinkConnectorName());
        assertNotEquals(first.sourceTable(), second.sourceTable());
        assertNotEquals(first.sinkTable(), second.sinkTable());
        assertNotEquals(first.slotName(), second.slotName());
        assertNotEquals(first.topicName(), second.topicName());
        assertNotEquals(first.topicResourceName(), second.topicResourceName());
    }

    @Test
    void createRunResourcesProducesSafeNamesWithBounds() {
        String noisyToken = "RUN-ID__WITH.invalid/chars-and-super-long-0123456789abcdefghijklmnopqrstuvwxyz";
        CdcIntegrationService.CdcRunResources resources = CdcIntegrationService.createRunResources(noisyToken);

        Pattern k8sNamePattern = Pattern.compile("^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$");
        Pattern postgresIdentifierPattern = Pattern.compile("^[a-z][a-z0-9_]*$");

        assertTrue(resources.runToken().matches("^[a-z0-9]+$"));
        assertTrue(resources.runToken().length() <= 32);

        assertTrue(resources.sourceConnectorName().length() <= 63);
        assertTrue(resources.sinkConnectorName().length() <= 63);
        assertTrue(resources.topicResourceName().length() <= 63);
        assertTrue(k8sNamePattern.matcher(resources.sourceConnectorName()).matches());
        assertTrue(k8sNamePattern.matcher(resources.sinkConnectorName()).matches());
        assertTrue(k8sNamePattern.matcher(resources.topicResourceName()).matches());

        assertTrue(resources.sourceTable().length() <= 63);
        assertTrue(resources.sinkTable().length() <= 63);
        assertTrue(resources.slotName().length() <= 63);
        assertTrue(postgresIdentifierPattern.matcher(resources.sourceTable()).matches());
        assertTrue(postgresIdentifierPattern.matcher(resources.sinkTable()).matches());
        assertTrue(postgresIdentifierPattern.matcher(resources.slotName()).matches());

        assertEquals("cdc.public." + resources.sourceTable(), resources.topicName());
        assertFalse(resources.topicResourceName().contains("_"));
    }

    @Test
    void createRunResourcesFallsBackToGeneratedTokenWhenInputIsInvalid() {
        CdcIntegrationService.CdcRunResources resources = CdcIntegrationService.createRunResources("---////___");

        assertEquals(32, resources.runToken().length());
        assertTrue(resources.runToken().matches("^[a-z0-9]+$"));
    }
}
