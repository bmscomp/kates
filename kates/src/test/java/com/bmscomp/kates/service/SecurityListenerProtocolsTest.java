package com.bmscomp.kates.service;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.anyCollection;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.DescribeTopicsResult;
import org.apache.kafka.clients.admin.ListTopicsResult;
import org.apache.kafka.common.KafkaFuture;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;

/**
 * The audit checks and pentest probes that turn on what the listeners are.
 * They read sasl.enabled.mechanisms, which Kafka reports as GSSAPI on a broker
 * with no SASL listener, so a PLAINTEXT broker passed SASL Authentication, and
 * looked for the text PLAINTEXT:// in listeners, which Strimzi's PLAIN-9092
 * (SCRAM without TLS) never holds, so it passed No Plaintext Listeners. They
 * now take each listener's protocol from listener.security.protocol.map.
 */
class SecurityListenerProtocolsTest {

    private static final String DEFAULT_PROTOCOL_MAP =
            "PLAINTEXT:PLAINTEXT,SSL:SSL,SASL_PLAINTEXT:SASL_PLAINTEXT,SASL_SSL:SASL_SSL";

    private Map<String, String> config = Map.of();
    private SecurityService service;
    private SecurityPentestService pentest;

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

        // A real helpers bean answering with the test's broker config: a mock
        // would also stub formatBytes, which the checks print with.
        SecurityKafkaHelpers helpers = new SecurityKafkaHelpers() {
            @Override
            public Map<String, String> fetchBrokerConfig(AdminClient c, int brokerId) {
                return config;
            }
        };
        service = new SecurityService(admin, health);
        service.helpers = helpers;
        pentest = new SecurityPentestService(admin, health, helpers);
    }

    /** The kafka-cluster chart's Strimzi broker: plain is SCRAM without TLS, tls and replication are TLS. */
    @ParameterizedTest
    @ValueSource(strings = {"", "GSSAPI"})
    void strimziChartListeners(String brokerWideMechanisms) {
        config = Map.of(
                "listeners",
                "REPLICATION-9091://0.0.0.0:9091,PLAIN-9092://0.0.0.0:9092,TLS-9093://0.0.0.0:9093",
                "listener.security.protocol.map",
                "CONTROLPLANE-9090:SSL,REPLICATION-9091:SSL,PLAIN-9092:SASL_PLAINTEXT,TLS-9093:SSL",
                "sasl.enabled.mechanisms",
                brokerWideMechanisms,
                "listener.name.plain-9092.sasl.enabled.mechanisms",
                "SCRAM-SHA-512");

        Map<String, Map<String, Object>> checks = audit();
        assertStatus(checks, "SASL Authentication", "PASS");
        assertStatus(checks, "No Plaintext Listeners", "WARN");
        assertTrue(detail(checks, "No Plaintext Listeners").contains("PLAIN-9092 (SASL_PLAINTEXT)"));
        assertFalse(detail(checks, "No Plaintext Listeners").contains("TLS-9093"), "an SSL listener is encrypted");
        assertStatus(checks, "Listener Protocol Map", "WARN");
        assertStatus(checks, "SASL Mechanism Strength", "PASS");
        assertEquals("Mechanisms: SCRAM-SHA-512", detail(checks, "SASL Mechanism Strength"));

        Map<String, String> probes = pentest();
        assertEquals("PROTECTED", probes.get("metadata-leak"));
        assertEquals("VULNERABLE", probes.get("unencrypted"));
    }

    /** A broker that sets only listeners: Kafka reports its default map and GSSAPI. */
    @Test
    void vanillaPlaintextDefaultGssapi() {
        config = Map.of(
                "listeners", "PLAINTEXT://:9092",
                "listener.security.protocol.map", DEFAULT_PROTOCOL_MAP,
                "sasl.enabled.mechanisms", "GSSAPI");

        Map<String, Map<String, Object>> checks = audit();
        assertStatus(checks, "SASL Authentication", "FAIL");
        assertStatus(checks, "SASL Mechanism Strength", "WARN");
        assertEquals("No SASL listener", detail(checks, "SASL Mechanism Strength"));
        assertStatus(checks, "No Plaintext Listeners", "WARN");
        assertStatus(checks, "Listener Protocol Map", "WARN");

        Map<String, String> probes = pentest();
        assertEquals("VULNERABLE", probes.get("metadata-leak"));
        assertEquals("VULNERABLE", probes.get("unencrypted"));
    }

    @Test
    void saslSslOnly() {
        config = Map.of(
                "listeners", "SASL_SSL://:9094",
                "listener.security.protocol.map", DEFAULT_PROTOCOL_MAP,
                "sasl.enabled.mechanisms", "SCRAM-SHA-512");

        Map<String, Map<String, Object>> checks = audit();
        assertStatus(checks, "SASL Authentication", "PASS");
        assertStatus(checks, "No Plaintext Listeners", "PASS");
        assertStatus(checks, "Listener Protocol Map", "PASS");
        assertStatus(checks, "SASL Mechanism Strength", "PASS");

        Map<String, String> probes = pentest();
        assertEquals("PROTECTED", probes.get("metadata-leak"));
        assertEquals("PROTECTED", probes.get("unencrypted"));
    }

    /** A PLAINTEXT listener beside a SASL one still lets any client in. */
    @Test
    void plaintextBesideSasl() {
        config = Map.of(
                "listeners", "PLAINTEXT://:9092,SASL_SSL://:9094",
                "listener.security.protocol.map", DEFAULT_PROTOCOL_MAP,
                "sasl.enabled.mechanisms", "SCRAM-SHA-512");

        Map<String, Map<String, Object>> checks = audit();
        assertStatus(checks, "SASL Authentication", "FAIL");
        assertTrue(detail(checks, "SASL Authentication").contains("PLAINTEXT (PLAINTEXT)"));
        assertEquals("VULNERABLE", pentest().get("metadata-leak"));
    }

    /** The audit's checks by name. runAudit grades an exception F, so a report with an error fails here. */
    private Map<String, Map<String, Object>> audit() {
        Map<String, Object> report = service.runAudit();
        assertFalse(report.containsKey("error"), () -> "the audit failed: " + report.get("error"));
        Map<String, Map<String, Object>> byName = new LinkedHashMap<>();
        for (Object check : (List<?>) report.get("checks")) {
            @SuppressWarnings("unchecked")
            Map<String, Object> c = (Map<String, Object>) check;
            byName.put((String) c.get("name"), c);
        }
        return byName;
    }

    /** Each pentest probe's result by id. */
    private Map<String, String> pentest() {
        Map<String, Object> report = pentest.pentest("all");
        assertFalse(report.containsKey("error"), () -> "the pentest failed: " + report.get("error"));
        Map<String, String> byId = new LinkedHashMap<>();
        for (Object test : (List<?>) report.get("tests")) {
            Map<?, ?> t = (Map<?, ?>) test;
            byId.put((String) t.get("id"), (String) t.get("result"));
        }
        return byId;
    }

    private static void assertStatus(Map<String, Map<String, Object>> checks, String name, String status) {
        assertEquals(
                status,
                checks.get(name).get("status"),
                name + ": " + checks.get(name).get("detail"));
    }

    private static String detail(Map<String, Map<String, Object>> checks, String name) {
        return (String) checks.get(name).get("detail");
    }
}
