package com.bmscomp.kates.compliance;

import static com.bmscomp.kates.compliance.KafkaReferenceExamplesTest.json;
import static org.junit.jupiter.api.Assertions.*;

import java.nio.file.Files;
import java.nio.file.Path;
import java.util.stream.Collectors;

import io.fabric8.kubernetes.client.Config;
import io.fabric8.kubernetes.client.ConfigBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.KubernetesClientBuilder;
import io.fabric8.kubernetes.client.KubernetesClientException;
import io.fabric8.kubernetes.client.vertx.VertxHttpClientFactory;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;

import com.bmscomp.kates.compliance.ComplianceReport.Result;
import com.bmscomp.kates.compliance.ComplianceReport.Status;
import com.bmscomp.kates.compliance.ComplianceReport.Verdict;

/**
 * Holds the CI cluster to cli/examples/conformity/kind-ci.yaml as a
 * rbac.scope=namespace install would read it: the job applies the kates
 * chart's RBAC for the ServiceAccount kates in two namespaces, `team` with
 * the Kafka-namespace Role only and `team-nodes` with rbac.nodeReads too, and
 * this test impersonates each. So the chart's grants are proven sufficient
 * against real Strimzi resources, which the mock server cannot show.
 */
@EnabledIfEnvironmentVariable(named = "KATES_COMPLIANCE_LIVE", matches = "true")
class ComplianceNamespacedLiveTest {

    @Test
    void theKafkaNamespaceRoleAloneReadsZonesFromEndpointSlices() throws Exception {
        try (KubernetesClient client = as("team")) {
            KubernetesClientException refused = assertThrows(
                    KubernetesClientException.class, () -> client.nodes().list());
            assertEquals(403, refused.getCode());

            ComplianceReport report = check(client);

            assertPasses(report);
            String zones = rule(report, "brokers.zones").detail();
            assertTrue(zones.endsWith("zones from the EndpointSlices of kafka/krafter-kafka-brokers"), zones);
        }
    }

    @Test
    void nodeReadsLetTheCheckReadTheNodes() throws Exception {
        try (KubernetesClient client = as("team-nodes")) {
            assertFalse(client.nodes().list().getItems().isEmpty());

            ComplianceReport report = check(client);

            assertPasses(report);
            String zones = rule(report, "brokers.zones").detail();
            assertFalse(zones.contains("zones from"), zones);
        }
    }

    /** The job's kubeconfig, impersonating the ServiceAccount kates in a namespace. */
    private static KubernetesClient as(String namespace) {
        Config config = new ConfigBuilder(Config.autoConfigure(null))
                .withImpersonateUsername("system:serviceaccount:" + namespace + ":kates")
                .withImpersonateGroups(
                        "system:serviceaccounts", "system:serviceaccounts:" + namespace, "system:authenticated")
                .build();
        return new KubernetesClientBuilder()
                .withConfig(config)
                .withHttpClientFactory(new VertxHttpClientFactory())
                .build();
    }

    private static ComplianceReport check(KubernetesClient client) throws Exception {
        KafkaReference reference = new KafkaReferenceReader()
                .read(json(Files.readString(Path.of("..", "cli", "examples", "conformity", "kind-ci.yaml"))));
        return new ComplianceCheck(client, null, "kafka", "krafter").check(reference);
    }

    private static void assertPasses(ComplianceReport report) {
        String gaps = report.results().stream()
                .filter(r -> r.status() != Status.PASS)
                .map(r -> r.status() + " " + r.rule() + ": " + r.detail())
                .collect(Collectors.joining("\n"));
        assertEquals(Verdict.PASS, report.verdict(), () -> "rules that do not pass:\n" + gaps);
        assertEquals("", gaps, "every rule passes, warn rules included");
    }

    private static Result rule(ComplianceReport report, String id) {
        return report.results().stream()
                .filter(r -> r.rule().equals(id))
                .findFirst()
                .orElseThrow();
    }
}
