package com.bmscomp.kates.service;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import java.util.List;
import java.util.Map;
import java.util.Optional;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * GET /api/security/cve compares its CVE list with the Kafka version. It read
 * the version from a cluster description that never holds one, so it compared
 * "unknown", which compareVersions cannot parse, and reported every CVE
 * PATCHED and the grade PASS on any cluster.
 */
class SecurityServiceCveTest {

    private SecurityService service;

    @BeforeEach
    void setUp() {
        service = new SecurityService(mock(KafkaAdminService.class), mock(ClusterHealthService.class));
        service.topology = mock(ClusterTopologyService.class);
    }

    @SuppressWarnings("unchecked")
    private static List<String> ids(Map<String, Object> report, String list) {
        return ((List<Map<String, Object>>) report.get(list))
                .stream().map(c -> (String) c.get("id")).toList();
    }

    @Test
    void anOldVersionIsVulnerableToWhatItPredates() {
        when(service.topology.kafkaVersion()).thenReturn(Optional.of("3.6.1"));

        Map<String, Object> report = service.cveCheck();

        assertEquals("3.6.1", report.get("kafkaVersion"));
        assertEquals("FAIL", report.get("grade"));
        assertEquals(List.of("CVE-2024-31141", "CVE-2024-27309"), ids(report, "vulnerabilities"));
        assertFalse(report.containsKey("reason"));
    }

    @Test
    void aCurrentVersionPasses() {
        when(service.topology.kafkaVersion()).thenReturn(Optional.of("4.1.0"));

        Map<String, Object> report = service.cveCheck();

        assertEquals("PASS", report.get("grade"));
        assertTrue(ids(report, "vulnerabilities").isEmpty());
        assertEquals(7, ids(report, "patched").size());
    }

    /** Without a version no CVE is ruled out, so the check fails and says why. */
    @Test
    void anUnknownVersionComparesNothingAndFails() {
        when(service.topology.kafkaVersion()).thenReturn(Optional.empty());

        Map<String, Object> report = service.cveCheck();

        assertEquals("unknown", report.get("kafkaVersion"));
        assertEquals("FAIL", report.get("grade"));
        assertTrue(ids(report, "patched").isEmpty(), "nothing is called patched");
        assertEquals(7, ids(report, "notCompared").size());
        assertEquals(Map.of("total", 7, "vulnerable", 0, "patched", 0, "notCompared", 7), report.get("summary"));
        assertTrue(((String) report.get("reason")).startsWith("The Kafka version could not be read"));
    }
}
