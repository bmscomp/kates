package com.bmscomp.kates.service;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.mockito.ArgumentMatchers.anyCollection;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import java.util.List;
import java.util.Map;
import java.util.Set;

import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.DescribeTopicsResult;
import org.apache.kafka.clients.admin.ListTopicsResult;
import org.apache.kafka.common.KafkaFuture;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * What feeds {@code GET /api/security/trend}. Compliance, baseline, drift and
 * gate each ran a full audit through {@code securityAudit()}, and every one of
 * those appended a snapshot, so the trend mostly counted API traffic: one
 * {@code kates security gate} per CI build, or one agent reading three
 * sections, moved it as much as an audit did.
 */
class SecurityServiceTrendTest {

    private SecurityService service;

    @BeforeEach
    void setUp() {
        AdminClient client = mock(AdminClient.class);
        ListTopicsResult topics = mock(ListTopicsResult.class);
        when(topics.names()).thenReturn(KafkaFuture.completedFuture(Set.of()));
        when(client.listTopics()).thenReturn(topics);
        DescribeTopicsResult described = mock(DescribeTopicsResult.class);
        when(described.allTopicNames()).thenReturn(KafkaFuture.completedFuture(Map.of()));
        when(client.describeTopics(anyCollection())).thenReturn(described);
        KafkaAdminService admin = mock(KafkaAdminService.class);
        when(admin.getClient()).thenReturn(client);

        ClusterHealthService health = mock(ClusterHealthService.class);
        when(health.describeCluster()).thenReturn(Map.of("brokerCount", 1, "brokers", List.of(Map.of("id", 0))));

        service = new SecurityService(admin, health);
        // Broker config and ACL reads fail against the mock client, and the
        // helpers answer those with empty results, as against a real broker
        // that refuses them: every check still runs and is graded.
        service.helpers = new SecurityKafkaHelpers();
    }

    @Test
    void onlyAnExplicitAuditAddsToTheTrend() {
        service.securityCompliance();
        service.securityGate("B");
        service.saveBaseline();
        service.securityDrift();

        assertEquals(0, service.scoreTrend().get("totalSnapshots"), "reuses of the audit must not record it");
        assertEquals("NO_DATA", service.scoreTrend().get("trend"));

        service.securityAudit();
        assertEquals(1, service.scoreTrend().get("totalSnapshots"));
    }

    @Test
    void aBetterGradeReadsAsImproving() throws Exception {
        record(Map.of("grade", "F"));
        record(Map.of("grade", "A"));
        assertEquals("IMPROVING", service.scoreTrend().get("trend"), "F then A");

        record(Map.of("grade", "C"));
        assertEquals("DEGRADING", service.scoreTrend().get("trend"), "A then C");

        record(Map.of("grade", "C"));
        assertEquals("STABLE", service.scoreTrend().get("trend"));
    }

    /** Puts a snapshot in the history as an audit of that grade would. */
    @SuppressWarnings("unchecked")
    private void record(Map<String, Object> snapshot) throws Exception {
        var field = SecurityService.class.getDeclaredField("scoreHistory");
        field.setAccessible(true);
        ((List<Map<String, Object>>) field.get(service)).add(snapshot);
    }

    @Test
    void theReportsStillCarryTheAuditTheyAreBuiltFrom() {
        Map<String, Object> audit = service.securityAudit();
        assertFalse(((List<?>) audit.get("checks")).isEmpty(), "the audit graded its checks");

        assertEquals(audit.get("grade"), service.securityCompliance().get("grade"));
        assertEquals(audit.get("grade"), service.securityGate("F").get("currentGrade"));
        assertEquals(audit.get("grade"), service.saveBaseline().get("grade"));
        assertEquals(audit.get("grade"), service.securityDrift().get("currentGrade"));
    }
}
