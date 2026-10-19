package com.bmscomp.kates.conformity;

import static com.bmscomp.kates.conformity.KafkaReferenceExamplesTest.json;
import static org.junit.jupiter.api.Assertions.*;

import java.io.IOException;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.List;
import java.util.Map;
import java.util.concurrent.TimeUnit;
import jakarta.ws.rs.ClientErrorException;

import io.fabric8.kubernetes.api.model.GenericKubernetesResource;
import io.fabric8.kubernetes.api.model.GenericKubernetesResourceBuilder;
import io.fabric8.kubernetes.api.model.Pod;
import io.fabric8.kubernetes.api.model.PodBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.VersionInfo;
import io.fabric8.kubernetes.client.server.mock.EnableKubernetesMockClient;
import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.api.ApiError;
import com.bmscomp.kates.chaos.VertxPerMockClient;
import com.bmscomp.kates.conformity.ConformityReport.Result;
import com.bmscomp.kates.conformity.ConformityReport.Status;
import com.bmscomp.kates.conformity.ConformityReport.Verdict;
import com.bmscomp.kates.service.ClusterTopologyService;

@EnableKubernetesMockClient(crud = true, kubernetesClientBuilderCustomizer = VertxPerMockClient.class)
class ConformityCheckTest {

    static final String HEADER =
            "apiVersion: conformity.kates.io/v1alpha1\nkind: KafkaReference\nmetadata: {name: t}\n";

    KubernetesMockServer server;
    KubernetesClient client;

    ConformityCheck check;

    @BeforeEach
    void cluster() {
        check = new ConformityCheck(client, "kafka", "krafter");
        check.clock = Clock.fixed(Instant.parse("2026-10-19T12:00:00Z"), ZoneOffset.UTC);
        server.setVersionInfo(new VersionInfo.Builder()
                .withMajor("1")
                .withMinor("34")
                .withGitVersion("v1.34.11+k3s1")
                .build());
        create(
                ClusterTopologyService.KAFKA_CRD,
                kafka(Map.of(
                        "clusterId", "4L6g3nShT-eMCtK--X86sw",
                        "kafkaVersion", "4.3.1",
                        "operatorLastSuccessfulVersion", "1.2.0",
                        "observedGeneration", 1,
                        "conditions", List.of(Map.of("type", "Ready", "status", "True")))));
        create(ClusterTopologyService.NODE_POOL_CRD, pool("controllers", "controller"));
        create(ClusterTopologyService.NODE_POOL_CRD, pool("brokers", "broker"));
        for (int i = 0; i < 3; i++) {
            pod("krafter-controllers-" + i, false, true, true);
            pod("krafter-brokers-" + (i + 3), true, false, i != 2);
        }
    }

    static GenericKubernetesResource kafka(Map<String, Object> status) {
        return new GenericKubernetesResourceBuilder()
                .withApiVersion("kafka.strimzi.io/v1")
                .withKind("Kafka")
                .withNewMetadata()
                .withName("krafter")
                .withNamespace("kafka")
                .withGeneration(1L)
                .endMetadata()
                .withAdditionalProperties(Map.of("spec", Map.of(), "status", status))
                .build();
    }

    static GenericKubernetesResource pool(String name, String... roles) {
        return new GenericKubernetesResourceBuilder()
                .withApiVersion("kafka.strimzi.io/v1")
                .withKind("KafkaNodePool")
                .withNewMetadata()
                .withName(name)
                .withNamespace("kafka")
                .addToLabels("strimzi.io/cluster", "krafter")
                .endMetadata()
                .withAdditionalProperties(Map.of("spec", Map.of("roles", List.of(roles), "replicas", 3)))
                .build();
    }

    void create(io.fabric8.kubernetes.client.dsl.base.ResourceDefinitionContext crd, GenericKubernetesResource r) {
        client.genericKubernetesResources(crd).inNamespace("kafka").resource(r).create();
    }

    Pod pod(String name, boolean broker, boolean controller, boolean ready) {
        Pod pod = new PodBuilder()
                .withNewMetadata()
                .withName(name)
                .withNamespace("kafka")
                .addToLabels("strimzi.io/cluster", "krafter")
                .addToLabels("strimzi.io/kind", "Kafka")
                .addToLabels("strimzi.io/broker-role", String.valueOf(broker))
                .addToLabels("strimzi.io/controller-role", String.valueOf(controller))
                .endMetadata()
                .withNewStatus()
                .addNewCondition()
                .withType("Ready")
                .withStatus(ready ? "True" : "False")
                .endCondition()
                .endStatus()
                .build();
        return client.pods().inNamespace("kafka").resource(pod).create();
    }

    ConformityReport run(String spec) throws IOException {
        return check.check(new KafkaReferenceReader().read(json(HEADER + "spec: " + spec)));
    }

    static Result result(ConformityReport report, String rule) {
        return report.results().stream()
                .filter(r -> r.rule().equals(rule))
                .findFirst()
                .orElseThrow();
    }

    @Test
    void passesWhenEveryRuleHolds() throws IOException {
        ConformityReport report = run("{versions: {kubernetes: {min: '1.32'}, strimzi: '1.2.0', kafka: {min: '4.2.0'}},"
                + " controllers: {count: 3, dedicated: true}, brokers: {count: {min: 3}}}");

        assertEquals(Verdict.PASS, report.verdict(), report::toString);
        assertEquals(6, report.summary().pass());
        assertEquals("1.34.11", result(report, "versions.kubernetes").observed());
        assertEquals("2 of 3 Ready", result(report, "brokers.count").detail());
        assertEquals(
                new ConformityReport.Target("strimzi", "kafka", "krafter", "4L6g3nShT-eMCtK--X86sw"), report.target());
        assertEquals(new ConformityReport.ClusterState(true, 1L, 1L, 1), report.cluster());
    }

    @Test
    void failsWithWhatTheClusterHas() throws IOException {
        ConformityReport report = run("{brokers: {count: {min: 5}}, controllers: {dedicated: false}}");

        assertEquals(Verdict.FAIL, report.verdict());
        Result count = result(report, "brokers.count");
        assertEquals(Status.FAIL, count.status());
        assertEquals(Map.of("min", 5), count.expected());
        assertEquals(3, count.observed());
        assertEquals(Status.FAIL, result(report, "controllers.dedicated").status());
    }

    @Test
    void aWarnRuleDoesNotFailTheCheck() throws IOException {
        ConformityReport report = run("{brokers: {count: {min: 5, severity: warn}}}");

        assertEquals(Status.WARN, report.results().get(0).status());
        assertEquals(Verdict.PASS, report.verdict());
    }

    @Test
    void anUnreadableSourceIsUnknownNotFail() throws IOException {
        client.genericKubernetesResources(ClusterTopologyService.KAFKA_CRD)
                .inNamespace("kafka")
                .withName("krafter")
                .delete();
        create(ClusterTopologyService.KAFKA_CRD, kafka(Map.of("kafkaVersion", "unknown")));

        ConformityReport report =
                run("{versions: {kafka: {min: '4.2.0'}, strimzi: {min: '1.2.0'}}, brokers: {count: 3}}");

        assertEquals(Verdict.UNKNOWN, report.verdict());
        assertEquals(
                "cannot read a version from unknown",
                result(report, "versions.kafka").detail());
        assertEquals(Status.UNKNOWN, result(report, "versions.strimzi").status());
        assertEquals(Status.PASS, result(report, "brokers.count").status());
        assertEquals(Status.UNKNOWN, report.results().get(0).status(), "UNKNOWN sorts before PASS");
    }

    @Test
    void anActiveWaiverKeepsTheGapVisible() throws IOException {
        ConformityReport report = run("{brokers: {count: 5}, controllers: {count: 3},"
                + " waivers: [{rule: brokers.count, reason: OPS-1, by: me, until: 2026-10-19},"
                + " {rule: controllers.count, reason: r, by: me, until: 2026-12-31}]}");

        Result waived = result(report, "brokers.count");
        assertEquals(Status.WAIVED, waived.status());
        assertEquals(Status.FAIL, waived.waiver().status());
        assertEquals(Verdict.PASS, report.verdict());
        assertEquals(
                "waiver no longer needed", result(report, "controllers.count").note());
    }

    @Test
    void anExpiredWaiverCountsAgain() throws IOException {
        ConformityReport report =
                run("{brokers: {count: 5}, waivers: [{rule: brokers.count, reason: r, by: me, until: 2026-10-18}]}");

        assertEquals(Status.FAIL, report.results().get(0).status());
        assertEquals("waiver expired on 2026-10-18", report.results().get(0).note());
        assertEquals(Verdict.FAIL, report.verdict());
    }

    @Test
    void refusesAReferenceForAnotherCluster() {
        assertEquals(
                409,
                assertThrows(ClientErrorException.class, () -> run("{target: {name: other}, brokers: {count: 3}}"))
                        .getResponse()
                        .getStatus());
        assertEquals(
                409,
                assertThrows(
                                ClientErrorException.class,
                                () -> run("{target: {clusterId: AAAAAAAAAAAAAAAAAAAAAA}, brokers: {count: 3}}"))
                        .getResponse()
                        .getStatus());
    }

    @Test
    void refusesWhenTheKafkaIsMissing() {
        client.genericKubernetesResources(ClusterTopologyService.KAFKA_CRD)
                .inNamespace("kafka")
                .withName("krafter")
                .delete();

        ClientErrorException e = assertThrows(ClientErrorException.class, () -> run("{brokers: {count: 3}}"));
        assertEquals(409, e.getResponse().getStatus());
        assertEquals("No Kafka kafka/krafter in the cluster", e.getMessage());
    }

    @Test
    void refusesARuleItCannotEvaluateYet() {
        ApiError error = assertThrows(InvalidReferenceException.class, () -> run("{brokers: {zones: 3}}"))
                .getError();

        assertEquals(400, error.getStatus());
        assertEquals(Map.of("spec.brokers.zones", "not implemented yet"), error.getFieldErrors());
    }

    @Test
    void onlyReads() throws Exception {
        int before = server.getRequestCount();
        for (int i = 0; i < before; i++) {
            server.takeRequest(1, TimeUnit.SECONDS);
        }

        run("{versions: {kubernetes: {min: '1.32'}}, controllers: {count: 3, dedicated: true}}");

        int requests = server.getRequestCount() - before;
        assertTrue(requests > 0);
        for (int i = 0; i < requests; i++) {
            var request = server.takeRequest(1, TimeUnit.SECONDS);
            assertEquals("GET", request.getMethod(), request.getPath());
        }
    }
}
