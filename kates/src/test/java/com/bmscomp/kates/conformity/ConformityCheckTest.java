package com.bmscomp.kates.conformity;

import static com.bmscomp.kates.conformity.KafkaReferenceExamplesTest.json;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyCollection;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.verifyNoInteractions;
import static org.mockito.Mockito.when;

import java.io.IOException;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.Collection;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.TimeUnit;
import jakarta.ws.rs.ClientErrorException;
import jakarta.ws.rs.ServiceUnavailableException;

import io.fabric8.kubernetes.api.model.AffinityBuilder;
import io.fabric8.kubernetes.api.model.GenericKubernetesResource;
import io.fabric8.kubernetes.api.model.GenericKubernetesResourceBuilder;
import io.fabric8.kubernetes.api.model.LabelSelector;
import io.fabric8.kubernetes.api.model.LabelSelectorBuilder;
import io.fabric8.kubernetes.api.model.NodeBuilder;
import io.fabric8.kubernetes.api.model.PersistentVolumeClaimBuilder;
import io.fabric8.kubernetes.api.model.Pod;
import io.fabric8.kubernetes.api.model.PodAffinityTerm;
import io.fabric8.kubernetes.api.model.PodAffinityTermBuilder;
import io.fabric8.kubernetes.api.model.PodBuilder;
import io.fabric8.kubernetes.api.model.Quantity;
import io.fabric8.kubernetes.api.model.VolumeBuilder;
import io.fabric8.kubernetes.api.model.discovery.v1.Endpoint;
import io.fabric8.kubernetes.api.model.discovery.v1.EndpointBuilder;
import io.fabric8.kubernetes.api.model.discovery.v1.EndpointSliceBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.VersionInfo;
import io.fabric8.kubernetes.client.server.mock.EnableKubernetesMockClient;
import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;
import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.Config;
import org.apache.kafka.clients.admin.ConfigEntry;
import org.apache.kafka.clients.admin.DescribeClusterResult;
import org.apache.kafka.clients.admin.DescribeConfigsResult;
import org.apache.kafka.clients.admin.DescribeTopicsResult;
import org.apache.kafka.clients.admin.ListTopicsOptions;
import org.apache.kafka.clients.admin.ListTopicsResult;
import org.apache.kafka.clients.admin.TopicDescription;
import org.apache.kafka.common.KafkaFuture;
import org.apache.kafka.common.Node;
import org.apache.kafka.common.TopicPartitionInfo;
import org.apache.kafka.common.config.ConfigResource;
import org.apache.kafka.common.errors.TimeoutException;
import org.apache.kafka.common.internals.KafkaFutureImpl;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.mockito.ArgumentCaptor;

import com.bmscomp.kates.chaos.VertxPerMockClient;
import com.bmscomp.kates.conformity.ConformityReport.Result;
import com.bmscomp.kates.conformity.ConformityReport.Status;
import com.bmscomp.kates.conformity.ConformityReport.Verdict;
import com.bmscomp.kates.service.ClusterTopologyService;
import com.bmscomp.kates.service.KafkaAdminService;

@EnableKubernetesMockClient(crud = true, kubernetesClientBuilderCustomizer = VertxPerMockClient.class)
class ConformityCheckTest {

    static final String HEADER =
            "apiVersion: conformity.kates.io/v1alpha1\nkind: KafkaReference\nmetadata: {name: t}\n";

    KubernetesMockServer server;
    KubernetesClient client;

    ConformityCheck check;
    KafkaAdminService admin;

    @BeforeEach
    void cluster() {
        admin = mock(KafkaAdminService.class);
        check = new ConformityCheck(client, admin, "kafka", "krafter");
        check.clock = Clock.fixed(Instant.parse("2026-10-19T12:00:00Z"), ZoneOffset.UTC);
        server.setVersionInfo(new VersionInfo.Builder()
                .withMajor("1")
                .withMinor("34")
                .withGitVersion("v1.34.11+k3s1")
                .build());
        create(
                ClusterTopologyService.KAFKA_CRD,
                kafka(
                        KAFKA_SPEC,
                        Map.of(
                                "clusterId", "4L6g3nShT-eMCtK--X86sw",
                                "kafkaVersion", "4.3.1",
                                "operatorLastSuccessfulVersion", "1.2.0",
                                "observedGeneration", 1,
                                "conditions", List.of(Map.of("type", "Ready", "status", "True")))));
        create(ClusterTopologyService.NODE_POOL_CRD, pool("controllers", "persistent-claim", "controller"));
        create(ClusterTopologyService.NODE_POOL_CRD, pool("brokers", "jbod", "broker"));
        for (int i = 0; i < 3; i++) {
            pod("krafter-controllers-" + i, false, true, true, "data-krafter-controllers-" + i);
            claim("data-krafter-controllers-" + i, "10Gi", "standard", true);
            String broker = "krafter-brokers-" + (i + 3);
            pod(broker, true, false, i != 2, "data-0-" + broker, "data-1-" + broker);
            claim("data-0-" + broker, "100Gi", "gp3", true);
            claim("data-1-" + broker, "100Gi", i == 0 ? "standard" : "gp3", i != 1);
        }
    }

    /** As the kafka-cluster chart renders it: a plain SCRAM listener and a TLS one, zone racks, simple authorization. */
    static final Map<String, Object> KAFKA_SPEC = Map.of(
            "kafka",
            Map.of(
                    "rack",
                    Map.of("topologyKey", "topology.kubernetes.io/zone"),
                    "authorization",
                    Map.of("type", "simple"),
                    "listeners",
                    List.of(
                            Map.of(
                                    "name",
                                    "plain",
                                    "port",
                                    9092,
                                    "type",
                                    "internal",
                                    "tls",
                                    false,
                                    "authentication",
                                    Map.of("type", "scram-sha-512")),
                            Map.of(
                                    "name",
                                    "tls",
                                    "port",
                                    9093,
                                    "type",
                                    "internal",
                                    "tls",
                                    true,
                                    "authentication",
                                    Map.of("type", "tls"))),
                    "config",
                    Map.of(
                            "min.insync.replicas", 2,
                            "default.replication.factor", 3,
                            "unclean.leader.election.enable", false)));

    static GenericKubernetesResource kafka(Map<String, Object> spec, Map<String, Object> status) {
        return new GenericKubernetesResourceBuilder()
                .withApiVersion("kafka.strimzi.io/v1")
                .withKind("Kafka")
                .withNewMetadata()
                .withName("krafter")
                .withNamespace("kafka")
                .withGeneration(1L)
                .endMetadata()
                .withAdditionalProperties(Map.of("spec", spec, "status", status))
                .build();
    }

    static GenericKubernetesResource pool(String name, String storageType, String... roles) {
        return pool(name, 3, storageType, roles);
    }

    static GenericKubernetesResource pool(String name, int replicas, String storageType, String... roles) {
        return new GenericKubernetesResourceBuilder()
                .withApiVersion("kafka.strimzi.io/v1")
                .withKind("KafkaNodePool")
                .withNewMetadata()
                .withName(name)
                .withNamespace("kafka")
                .addToLabels("strimzi.io/cluster", "krafter")
                .endMetadata()
                .withAdditionalProperties(Map.of(
                        "spec",
                        Map.of("roles", List.of(roles), "replicas", replicas, "storage", Map.of("type", storageType))))
                .build();
    }

    void create(io.fabric8.kubernetes.client.dsl.base.ResourceDefinitionContext crd, GenericKubernetesResource r) {
        client.genericKubernetesResources(crd).inNamespace("kafka").resource(r).create();
    }

    void claim(String name, String size, String storageClass, boolean bound) {
        var pvc = new PersistentVolumeClaimBuilder()
                .withNewMetadata()
                .withName(name)
                .withNamespace("kafka")
                .addToLabels("strimzi.io/cluster", "krafter")
                .endMetadata()
                .withNewSpec()
                .withStorageClassName(storageClass)
                .endSpec()
                .withNewStatus()
                .withPhase(bound ? "Bound" : "Pending")
                .addToCapacity("storage", new Quantity(size))
                .endStatus()
                .build();
        client.persistentVolumeClaims().inNamespace("kafka").resource(pvc).create();
    }

    Pod pod(String name, boolean broker, boolean controller, boolean ready, String... claims) {
        Pod pod = new PodBuilder()
                .withNewMetadata()
                .withName(name)
                .withNamespace("kafka")
                .addToLabels("strimzi.io/cluster", "krafter")
                .addToLabels("strimzi.io/kind", "Kafka")
                .addToLabels("strimzi.io/broker-role", String.valueOf(broker))
                .addToLabels("strimzi.io/controller-role", String.valueOf(controller))
                .endMetadata()
                .withNewSpec()
                .addNewContainer()
                .withName("kafka")
                .withNewResources()
                .addToRequests("cpu", new Quantity(broker ? "1" : "500m"))
                .addToRequests("memory", new Quantity(broker ? "4Gi" : "1Gi"))
                .addToLimits("memory", new Quantity(broker ? "4Gi" : "1Gi"))
                .endResources()
                .endContainer()
                .withVolumes(java.util.Arrays.stream(claims)
                        .map(c -> new VolumeBuilder()
                                .withName(c)
                                .withNewPersistentVolumeClaim()
                                .withClaimName(c)
                                .endPersistentVolumeClaim()
                                .build())
                        .toList())
                .endSpec()
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
        create(ClusterTopologyService.KAFKA_CRD, kafka(KAFKA_SPEC, Map.of("kafkaVersion", "unknown")));

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
    void sizesEachPodsKafkaContainer() throws IOException {
        ConformityReport report = run("{controllers: {requests: {cpu: {min: 500m}, memory: {min: 1Gi}}},"
                + " brokers: {requests: {memory: {min: 8Gi}}, limits: {cpu: {min: 1}}}}");

        assertEquals(Status.PASS, result(report, "controllers.requests.cpu").status());
        Result memory = result(report, "brokers.requests.memory");
        assertEquals(Status.FAIL, memory.status());
        assertEquals("4Gi", memory.observed());
        assertEquals(List.of("krafter-brokers-3", "krafter-brokers-4", "krafter-brokers-5"), memory.subjects());
        assertEquals("unset", result(report, "brokers.limits.cpu").observed());
    }

    @Test
    void addsUpTheBoundVolumesOfEachPod() throws IOException {
        ConformityReport report = run("{controllers: {storage: {type: persistent-claim, size: {min: 10Gi}}},"
                + " brokers: {storage: {type: jbod, size: {min: 200Gi}, class: gp3}}}");

        assertEquals(Status.PASS, result(report, "controllers.storage.type").status());
        assertEquals(Status.PASS, result(report, "controllers.storage.size").status());
        assertEquals(Status.PASS, result(report, "brokers.storage.type").status());
        Result size = result(report, "brokers.storage.size");
        assertEquals(Status.FAIL, size.status(), size::toString);
        assertEquals("100Gi", size.observed(), "the unbound volume adds nothing");
        assertEquals(List.of("krafter-brokers-4"), size.subjects());
        Result storageClass = result(report, "brokers.storage.class");
        assertEquals("standard", storageClass.observed());
        assertEquals(List.of("data-1-krafter-brokers-3"), storageClass.subjects());
    }

    @Test
    void storageClassFailsOnEphemeralStorage() throws IOException {
        for (int i = 3; i <= 5; i++) {
            client.pods()
                    .inNamespace("kafka")
                    .withName("krafter-brokers-" + i)
                    .edit(p ->
                            new PodBuilder(p).editSpec().withVolumes().endSpec().build());
        }

        Result storageClass = result(run("{brokers: {storage: {class: gp3}}}"), "brokers.storage.class");

        assertEquals(Status.FAIL, storageClass.status());
        assertNull(storageClass.observed());
        assertEquals("no PVCs: ephemeral storage", storageClass.detail());
    }

    /** A per-pod rule on a role with no pod has nothing to check, and does not pass; perNode's limit still holds. */
    @Test
    void aRoleWithNoPodHasNothingToPass() throws IOException {
        client.pods()
                .inNamespace("kafka")
                .withLabel("strimzi.io/broker-role", "true")
                .delete();
        podMonitor("kafka", "brokers", Map.of("strimzi.io/cluster", "krafter"), null);

        ConformityReport report = run(
                "{brokers: {requests: {memory: {min: 1Gi}}, perNode: {max: 1}}," + " monitoring: {podMonitor: true}}");

        Result memory = result(report, "brokers.requests.memory");
        assertEquals(Status.FAIL, memory.status());
        assertEquals("no pod has strimzi.io/broker-role=true", memory.detail());
        assertEquals(Status.PASS, result(report, "brokers.perNode").status());
        Result podMonitor = result(report, "monitoring.podMonitor");
        assertEquals(Status.FAIL, podMonitor.status());
        assertEquals("no broker pod to select", podMonitor.detail());
    }

    /** An old pool scaled to 0, as Strimzi's storage and role changes leave one: it runs no node. */
    @Test
    void aPoolAtZeroReplicasDecidesNothing() throws IOException {
        create(ClusterTopologyService.NODE_POOL_CRD, pool("combined", 0, "jbod", "broker", "controller"));
        create(ClusterTopologyService.NODE_POOL_CRD, pool("old", 0, "persistent-claim", "broker"));

        ConformityReport report = run("{controllers: {dedicated: true}, brokers: {storage: {type: jbod}}}");

        Result type = result(report, "brokers.storage.type");
        assertEquals(Status.PASS, type.status(), type::toString);
        assertEquals("combined, old: 0 replicas, not counted", type.detail());
        Result dedicated = result(report, "controllers.dedicated");
        assertEquals(Status.PASS, dedicated.status());
        assertEquals("combined: 0 replicas, not counted", dedicated.detail());
    }

    @Test
    void failingSubjectsPastTheCapAreCounted() throws IOException {
        for (int i = 6; i < 56; i++) {
            pod("krafter-brokers-" + i, true, false, true);
        }

        Result memory = result(run("{brokers: {requests: {memory: {min: 8Gi}}}}"), "brokers.requests.memory");

        assertEquals(50, memory.subjects().size());
        assertEquals("53 fall short; the first 50 are listed", memory.detail());
    }

    /** Three zones, one node each; controllers one per node, brokers crowded: two on alpha's node, none on gamma's. */
    void place() {
        for (String zone : List.of("alpha", "sigma", "gamma")) {
            client.nodes()
                    .resource(new NodeBuilder()
                            .withNewMetadata()
                            .withName("node-" + zone)
                            .addToLabels("topology.kubernetes.io/zone", zone)
                            .endMetadata()
                            .build())
                    .create();
        }
        client.nodes()
                .resource(new NodeBuilder()
                        .withNewMetadata()
                        .withName("node-bare")
                        .endMetadata()
                        .build())
                .create();
        Map<String, String> nodes = Map.of(
                "krafter-controllers-0", "node-alpha",
                "krafter-controllers-1", "node-sigma",
                "krafter-controllers-2", "node-gamma",
                "krafter-brokers-3", "node-alpha",
                "krafter-brokers-4", "node-alpha",
                "krafter-brokers-5", "node-sigma");
        nodes.forEach((pod, node) -> client.pods()
                .inNamespace("kafka")
                .withName(pod)
                .edit(p -> new PodBuilder(p)
                        .editSpec()
                        .withNodeName(node)
                        .withAffinity(new AffinityBuilder()
                                .withNewPodAntiAffinity()
                                .addToRequiredDuringSchedulingIgnoredDuringExecution(
                                        pod.contains("controllers") ? term("strimzi.io/controller-role") : null)
                                .addNewPreferredDuringSchedulingIgnoredDuringExecution()
                                .withWeight(100)
                                .withPodAffinityTerm(term("strimzi.io/broker-role"))
                                .endPreferredDuringSchedulingIgnoredDuringExecution()
                                .endPodAntiAffinity()
                                .build())
                        .endSpec()
                        .build()));
    }

    static PodAffinityTerm term(String roleLabel) {
        return new PodAffinityTermBuilder()
                .withTopologyKey("kubernetes.io/hostname")
                .withNewLabelSelector()
                .addToMatchLabels(roleLabel, "true")
                .endLabelSelector()
                .build();
    }

    @Test
    void measuresTheSpreadOverZonesAndNodes() throws IOException {
        place();

        ConformityReport report =
                run("{controllers: {zones: 3, skew: {max: 0}, perNode: {max: 1}, antiAffinity: required},"
                        + " brokers: {zones: {min: 3}, skew: {max: 0}, perNode: {max: 1}, antiAffinity: required}}");

        for (String rule : List.of("zones", "skew", "perNode", "antiAffinity")) {
            assertEquals(Status.PASS, result(report, "controllers." + rule).status(), rule);
            assertEquals(Status.FAIL, result(report, "brokers." + rule).status(), rule);
        }
        assertEquals(2, result(report, "brokers.zones").observed());
        assertEquals("alpha: 2, sigma: 1", result(report, "brokers.zones").detail());
        assertEquals(1, result(report, "brokers.skew").observed());
        assertEquals(List.of("node-alpha"), result(report, "brokers.perNode").subjects());
        assertEquals("preferred", result(report, "brokers.antiAffinity").observed());
    }

    @Test
    void aPodWithoutAZoneFailsTheSpread() throws IOException {
        place();
        client.pods()
                .inNamespace("kafka")
                .withName("krafter-brokers-4")
                .edit(p -> new PodBuilder(p)
                        .editSpec()
                        .withNodeName("node-bare")
                        .endSpec()
                        .build());

        Result zones = result(run("{brokers: {zones: {min: 1}}}"), "brokers.zones");

        assertEquals(Status.FAIL, zones.status());
        assertEquals(List.of("krafter-brokers-4"), zones.subjects());
        assertEquals("alpha: 1, sigma: 1; 1 not scheduled on a node with topology.kubernetes.io/zone", zones.detail());
    }

    @Test
    void matchesLabelSelectorsAsKubernetesDoes() {
        Map<String, String> labels = Map.of("app", "kafka", "role", "broker");

        assertTrue(ConformityCheck.matches(selector(Map.of("app", "kafka")), labels));
        assertFalse(ConformityCheck.matches(selector(Map.of("app", "zk")), labels));
        assertFalse(ConformityCheck.matches(null, labels), "no selector selects nothing");
        assertTrue(ConformityCheck.matches(expression("role", "In", "broker", "controller"), labels));
        assertFalse(ConformityCheck.matches(expression("role", "NotIn", "broker"), labels));
        assertTrue(ConformityCheck.matches(expression("zone", "NotIn", "a"), labels));
        assertTrue(ConformityCheck.matches(expression("app", "Exists"), labels));
        assertFalse(ConformityCheck.matches(expression("app", "DoesNotExist"), labels));
        assertFalse(ConformityCheck.matches(expression("app", "Gt", "1"), labels));
    }

    static LabelSelector selector(Map<String, String> matchLabels) {
        return new LabelSelectorBuilder().withMatchLabels(matchLabels).build();
    }

    static LabelSelector expression(String key, String operator, String... values) {
        return new LabelSelectorBuilder()
                .addNewMatchExpression()
                .withKey(key)
                .withOperator(operator)
                .withValues(values)
                .endMatchExpression()
                .build();
    }

    @Test
    void readsTheKafkaRulesFromTheKafkaCr() throws IOException {
        place();

        ConformityReport report = run("{kafka: {rackAwareness: true, authorization: {oneOf: [simple, custom]},"
                + " listeners: {require: [{name: tls, port: 9093, tls: true, auth: tls},"
                + " {name: plain, tls: true}, {name: external, type: route, severity: warn}],"
                + " every: {tls: true, auth: {oneOf: [tls, scram-sha-512]}}},"
                + " config: {min.insync.replicas: {min: 2}, unclean.leader.election.enable: false,"
                + " auto.create.topics.enable: false}}}");

        assertEquals(Status.PASS, result(report, "kafka.rackAwareness").status());
        assertEquals(Status.PASS, result(report, "kafka.authorization").status());
        assertEquals(Status.PASS, result(report, "kafka.listeners.require[tls]").status());
        Result plain = result(report, "kafka.listeners.require[plain]");
        assertEquals(Status.FAIL, plain.status());
        assertEquals("tls is false", plain.detail());
        Result external = result(report, "kafka.listeners.require[external]");
        assertEquals(Status.WARN, external.status());
        assertEquals("missing", external.observed());
        assertEquals(
                List.of("plain"), result(report, "kafka.listeners.every.tls").subjects());
        assertEquals(Status.PASS, result(report, "kafka.listeners.every.auth").status());
        assertEquals(
                Status.PASS, result(report, "kafka.config[min.insync.replicas]").status());
        assertEquals(
                Status.PASS,
                result(report, "kafka.config[unclean.leader.election.enable]").status());
        Result unset = result(report, "kafka.config[auto.create.topics.enable]");
        assertEquals(Status.FAIL, unset.status());
        assertEquals("not set in the Kafka CR", unset.detail());
    }

    @Test
    void rackAwarenessNeedsTheZoneKeyAndLabelledNodes() throws IOException {
        Result result =
                result(run("{zoneLabel: example.com/rack, kafka: {rackAwareness: true}}"), "kafka.rackAwareness");

        assertEquals(Status.FAIL, result.status());
        assertEquals(
                "spec.kafka.rack.topologyKey is topology.kubernetes.io/zone, not example.com/rack", result.detail());
    }

    /** Each broker's describeConfigs answer, by id; a broker asked for that is not here never answers. */
    Map<String, KafkaFuture<Config>> brokerConfigs = new java.util.HashMap<>();

    /**
     * Brokers 3 and 4 report a rack and min.insync.replicas 2; broker 5 reports neither a rack nor 2. The
     * cluster is the Kafka CR's.
     */
    AdminClient brokers() {
        AdminClient kafka = mock(AdminClient.class);
        when(admin.sharedAdminClient()).thenReturn(kafka);
        DescribeClusterResult cluster = mock(DescribeClusterResult.class);
        when(cluster.clusterId()).thenReturn(KafkaFuture.completedFuture("4L6g3nShT-eMCtK--X86sw"));
        when(cluster.nodes())
                .thenReturn(KafkaFuture.completedFuture(List.of(
                        new Node(3, "b3", 9092, "alpha"), new Node(4, "b4", 9092, "sigma"), new Node(5, "b5", 9092))));
        when(kafka.describeCluster()).thenReturn(cluster);
        for (int id = 3; id <= 5; id++) {
            brokerConfigs.put(
                    String.valueOf(id),
                    KafkaFuture.completedFuture(new Config(List.of(
                            new ConfigEntry("min.insync.replicas", id == 5 ? "1" : "2"),
                            new ConfigEntry("log.retention.ms", null),
                            new ConfigEntry(
                                    "ssl.keystore.password",
                                    null,
                                    ConfigEntry.ConfigSource.STATIC_BROKER_CONFIG,
                                    true,
                                    false,
                                    List.of(),
                                    ConfigEntry.ConfigType.PASSWORD,
                                    null)))));
        }
        when(kafka.describeConfigs(org.mockito.ArgumentMatchers.anyCollection()))
                .thenAnswer(call -> {
                    Map<ConfigResource, KafkaFuture<Config>> values = new java.util.HashMap<>();
                    for (ConfigResource asked : call.<java.util.Collection<ConfigResource>>getArgument(0)) {
                        values.put(asked, brokerConfigs.getOrDefault(asked.name(), new KafkaFutureImpl<>()));
                    }
                    DescribeConfigsResult described = mock(DescribeConfigsResult.class);
                    when(described.values()).thenReturn(values);
                    return described;
                });
        return kafka;
    }

    static <T> KafkaFuture<T> failed(Exception e) {
        KafkaFutureImpl<T> future = new KafkaFutureImpl<>();
        future.completeExceptionally(e);
        return future;
    }

    @Test
    void readsEffectiveValuesFromTheBrokersWithSourceBrokers() throws IOException {
        brokers();

        ConformityReport report = run("{kafka: {source: brokers, rackAwareness: true, config: {"
                + " min.insync.replicas: {min: 2}, auto.create.topics.enable: false, ssl.keystore.password: x}}}");

        Result rack = result(report, "kafka.rackAwareness");
        assertEquals(Status.FAIL, rack.status());
        assertEquals("brokers that report no rack: broker 5", rack.detail());
        Result minIsr = result(report, "kafka.config[min.insync.replicas]");
        assertEquals(Status.FAIL, minIsr.status());
        assertEquals("1", minIsr.observed());
        assertEquals(List.of("broker 5"), minIsr.subjects());
        assertEquals(
                "brokers do not report auto.create.topics.enable",
                result(report, "kafka.config[auto.create.topics.enable]").detail());
        Result secret = result(report, "kafka.config[ssl.keystore.password]");
        assertEquals(Status.UNKNOWN, secret.status());
        assertNull(secret.observed());
    }

    @Test
    void anAdminFailureIsUnknown() throws IOException {
        AdminClient kafka = mock(AdminClient.class);
        when(admin.sharedAdminClient()).thenReturn(kafka);
        DescribeClusterResult cluster = mock(DescribeClusterResult.class);
        when(cluster.clusterId()).thenReturn(failed(new TimeoutException("Timed out waiting")));
        when(kafka.describeCluster()).thenReturn(cluster);

        ConformityReport report = run("{kafka: {source: brokers, config: {min.insync.replicas: {min: 2}}}}");

        Result result = report.results().get(0);
        assertEquals(Status.UNKNOWN, result.status());
        assertTrue(result.detail().contains("Timed out waiting"), result.detail());
        assertEquals(Verdict.UNKNOWN, report.verdict());
    }

    @Test
    void brokersOfAnotherClusterAreUnknown() throws IOException {
        brokers();
        when(admin.sharedAdminClient().describeCluster().clusterId())
                .thenReturn(KafkaFuture.completedFuture("other-cluster-id-00000"));

        ConformityReport report = run("{kafka: {source: brokers, authorization: simple, rackAwareness: true,"
                + " config: {min.insync.replicas: {min: 2}}}}");

        for (String rule : List.of("kafka.rackAwareness", "kafka.config[min.insync.replicas]")) {
            Result result = result(report, rule);
            assertEquals(Status.UNKNOWN, result.status(), rule);
            assertTrue(
                    result.detail()
                            .endsWith("the Kafka Admin client (kates.kafka.bootstrap-servers) reaches cluster"
                                    + " other-cluster-id-00000; Kafka kafka/krafter is cluster 4L6g3nShT-eMCtK--X86sw"),
                    result.detail());
        }
        assertEquals(Status.PASS, result(report, "kafka.authorization").status(), "the Kafka CR is still read");
        assertEquals(Verdict.UNKNOWN, report.verdict());
    }

    /** Brokers 5 and 6 are in the pool, but Kafka reports only 3 and 4: fenced, or not started. */
    @Test
    void aBrokerKafkaDoesNotReportIsUnknownNotPass() throws IOException {
        brokers();
        when(admin.sharedAdminClient().describeCluster().nodes())
                .thenReturn(KafkaFuture.completedFuture(
                        List.of(new Node(3, "b3", 9092, "alpha"), new Node(4, "b4", 9092, "sigma"))));
        client.genericKubernetesResources(ClusterTopologyService.NODE_POOL_CRD)
                .inNamespace("kafka")
                .withName("brokers")
                .edit(p -> new GenericKubernetesResourceBuilder(p)
                        .addToAdditionalProperties("status", Map.of("nodeIds", List.of(3, 4, 5, 6)))
                        .build());

        ConformityReport report =
                run("{kafka: {source: brokers, rackAwareness: true, config: {min.insync.replicas: {min: 2}}}}");

        for (String rule : List.of("kafka.rackAwareness", "kafka.config[min.insync.replicas]")) {
            Result result = result(report, rule);
            assertEquals(Status.UNKNOWN, result.status(), rule);
            assertEquals("broker 5: not reported by Kafka; broker 6: not reported by Kafka", result.detail());
        }
    }

    /** Broker 4 does not answer: broker 5's gap is still a FAIL, and with no gap the rule is UNKNOWN. */
    @Test
    void aBrokerThatDoesNotAnswerHidesNoGap() throws IOException {
        brokers();
        brokerConfigs.put("4", failed(new TimeoutException("Timed out waiting for a node assignment")));

        Result strict = result(
                run("{kafka: {source: brokers, config: {min.insync.replicas: {min: 2}}}}"),
                "kafka.config[min.insync.replicas]");
        Result lax = result(
                run("{kafka: {source: brokers, config: {min.insync.replicas: {min: 1}}}}"),
                "kafka.config[min.insync.replicas]");

        assertEquals(Status.FAIL, strict.status());
        assertEquals(List.of("broker 5"), strict.subjects());
        assertEquals("broker 4: Timed out waiting for a node assignment", strict.detail());
        assertEquals(Status.UNKNOWN, lax.status());
        assertEquals("broker 4: Timed out waiting for a node assignment", lax.detail());
    }

    /** Kafka reports log.retention.ms with no value and derives it from log.retention.hours. */
    @Test
    void aConfigBrokersReportWithNoValueIsUnknownNotFail() throws IOException {
        brokers();

        Result retention = result(
                run("{kafka: {source: brokers, config: {log.retention.ms: {min: 604800000}}}}"),
                "kafka.config[log.retention.ms]");

        assertEquals(Status.UNKNOWN, retention.status());
        assertEquals(
                "broker 3, broker 4, broker 5 report no value: Kafka derives log.retention.ms from another setting",
                retention.detail());
    }

    /**
     * Four topics as Kafka describes them: one partition of payments has two
     * replicas and its min.insync.replicas is 1; Kates' probe topic has one
     * replica. Admin describes whichever of them it is asked about.
     */
    AdminClient topics() {
        Map<String, List<Integer>> replicas = Map.of(
                "__consumer_offsets", List.of(3),
                "orders", List.of(3, 3),
                "payments", List.of(3, 3, 2),
                "kates-probe-topic", List.of(1));
        Map<String, String> minIsr =
                Map.of("__consumer_offsets", "2", "orders", "2", "payments", "1", "kates-probe-topic", "1");
        AdminClient kafka = mock(AdminClient.class);
        when(admin.sharedAdminClient()).thenReturn(kafka);
        ListTopicsResult listed = mock(ListTopicsResult.class);
        when(listed.names()).thenReturn(KafkaFuture.completedFuture(replicas.keySet()));
        when(kafka.listTopics(any(ListTopicsOptions.class))).thenReturn(listed);
        when(kafka.describeTopics(anyCollection())).thenAnswer(call -> {
            Map<String, TopicDescription> described = new LinkedHashMap<>();
            for (String name : call.<Collection<String>>getArgument(0)) {
                List<TopicPartitionInfo> partitions = new ArrayList<>();
                for (int size : replicas.get(name)) {
                    List<Node> nodes = java.util.stream.IntStream.range(0, size)
                            .mapToObj(id -> new Node(id, "b" + id, 9092))
                            .toList();
                    partitions.add(new TopicPartitionInfo(partitions.size(), nodes.get(0), nodes, nodes));
                }
                described.put(name, new TopicDescription(name, name.startsWith("__"), partitions));
            }
            DescribeTopicsResult result = mock(DescribeTopicsResult.class);
            when(result.allTopicNames()).thenReturn(KafkaFuture.completedFuture(described));
            return result;
        });
        when(kafka.describeConfigs(anyCollection())).thenAnswer(call -> {
            Map<ConfigResource, Config> configs = new LinkedHashMap<>();
            for (ConfigResource topic : call.<Collection<ConfigResource>>getArgument(0)) {
                configs.put(
                        topic, new Config(List.of(new ConfigEntry("min.insync.replicas", minIsr.get(topic.name())))));
            }
            DescribeConfigsResult result = mock(DescribeConfigsResult.class);
            when(result.all()).thenReturn(KafkaFuture.completedFuture(configs));
            return result;
        });
        return kafka;
    }

    /** A topic's replication factor is its smallest replica set, so payments has 2. */
    @ParameterizedTest(name = "{0}")
    @CsvSource(delimiter = '|', nullValues = "-", textBlock = """
            {replicationFactor: {min: 3}}                                    | topics.replicationFactor | FAIL | [1, 2]    | kates-probe-topic, payments | 2 of 4 topics
            {replicationFactor: {min: 3}, exclude: [kates-]}                 | topics.replicationFactor | FAIL | 2         | payments                    | 1 of 3 topics
            {replicationFactor: {min: 3, severity: warn}, exclude: [kates-]} | topics.replicationFactor | WARN | 2         | payments                    | 1 of 3 topics
            {replicationFactor: {min: 1}}                                    | topics.replicationFactor | PASS | [3, 1, 2] | -                           | -
            {minInsyncReplicas: {min: 2}, exclude: [kates-]}                 | topics.minInsyncReplicas | FAIL | 1         | payments                    | 1 of 3 topics
            {minInsyncReplicas: {min: 2}, exclude: [kates-, pay]}            | topics.minInsyncReplicas | PASS | 2         | -                           | -
            {minInsyncReplicas: 2, exclude: [_, k, o, p]}                    | topics.minInsyncReplicas | PASS | -         | -                           | no topic to check
            """)
    void holdsEveryTopicToTheConstraint(
            String topics, String rule, Status status, String observed, String subjects, String detail)
            throws IOException {
        topics();

        Result result = result(run("{topics: " + topics + "}"), rule);

        assertEquals(status, result.status());
        assertEquals(
                observed, result.observed() == null ? null : result.observed().toString());
        assertEquals(subjects == null ? null : List.of(subjects.split(", ")), result.subjects());
        assertEquals(detail, result.detail());
    }

    /** listTopics with internal topics, describeTopics on those not excluded, one describeConfigs over them. */
    @Test
    void readsTopicsThroughAdminOnlyWhenDeclared() throws IOException {
        AdminClient kafka = topics();

        run("{brokers: {count: 3}, kafka: {config: {min.insync.replicas: {min: 2}}}}");
        verifyNoInteractions(kafka);

        run("{topics: {replicationFactor: {min: 3}, exclude: [kates-]}}");
        ArgumentCaptor<ListTopicsOptions> options = ArgumentCaptor.forClass(ListTopicsOptions.class);
        verify(kafka).listTopics(options.capture());
        assertTrue(options.getValue().shouldListInternal());
        verify(kafka).describeTopics(List.of("__consumer_offsets", "orders", "payments"));
        verify(kafka, never()).describeConfigs(anyCollection());

        run("{topics: {minInsyncReplicas: {min: 2}, exclude: [kates-]}}");
        verify(kafka)
                .describeConfigs(List.of(
                        new ConfigResource(ConfigResource.Type.TOPIC, "__consumer_offsets"),
                        new ConfigResource(ConfigResource.Type.TOPIC, "orders"),
                        new ConfigResource(ConfigResource.Type.TOPIC, "payments")));
    }

    @Test
    void anUnreachableAdminMakesTopicRulesUnknown() throws IOException {
        AdminClient kafka = mock(AdminClient.class);
        when(admin.sharedAdminClient()).thenReturn(kafka);
        KafkaFutureImpl<Set<String>> failed = new KafkaFutureImpl<>();
        failed.completeExceptionally(new org.apache.kafka.common.errors.TimeoutException("Timed out waiting"));
        ListTopicsResult listed = mock(ListTopicsResult.class);
        when(listed.names()).thenReturn(failed);
        when(kafka.listTopics(any(ListTopicsOptions.class))).thenReturn(listed);

        ConformityReport report = run("{topics: {replicationFactor: {min: 3}, minInsyncReplicas: {min: 2}}}");

        for (String rule : List.of("topics.replicationFactor", "topics.minInsyncReplicas")) {
            Result result = result(report, rule);
            assertEquals(Status.UNKNOWN, result.status(), rule);
            assertTrue(result.detail().contains("Timed out waiting"), result.detail());
        }
        assertEquals(Verdict.UNKNOWN, report.verdict());
    }

    /** Every rule the full example declares is evaluated: none is refused, and each has one result. */
    @Test
    void evaluatesEveryRuleOfTheFullExample() throws IOException {
        place();
        KafkaReference reference = new KafkaReferenceReader()
                .read(json(java.nio.file.Files.readString(
                        java.nio.file.Path.of("..", "cli", "examples", "conformity", "prod-3az.yaml"))));

        ConformityReport report = check.check(reference);

        assertEquals(
                KafkaReferenceReader.ruleIds(reference).size(), report.results().size());
        assertEquals(
                KafkaReferenceReader.ruleIds(reference),
                report.results().stream().map(Result::rule).collect(java.util.stream.Collectors.toSet()));
    }

    @Test
    void readsMonitoringFromTheKafkaCrAndThePodMonitors() throws IOException {
        podMonitor("kafka", "brokers", Map.of("strimzi.io/cluster", "krafter"), null);
        podMonitor("monitoring", "elsewhere", Map.of("strimzi.io/cluster", "krafter"), null);
        podMonitor("monitoring", "anywhere", Map.of("strimzi.io/broker-role", "true"), Map.of("any", true));

        ConformityReport report = run("{monitoring: {metrics: none, kafkaExporter: true, podMonitor: true}}");

        assertEquals(Status.PASS, result(report, "monitoring.metrics").status());
        assertEquals(Status.FAIL, result(report, "monitoring.kafkaExporter").status());
        Result podMonitor = result(report, "monitoring.podMonitor");
        assertEquals(Status.PASS, podMonitor.status());
        assertEquals("kafka/brokers, monitoring/anywhere", podMonitor.detail());
    }

    @Test
    void aPodMonitorThatSelectsNoBrokerDoesNotCount() throws IOException {
        podMonitor("kafka", "controllers", Map.of("strimzi.io/controller-role", "true"), null);

        Result result = result(run("{monitoring: {podMonitor: true}}"), "monitoring.podMonitor");

        assertEquals(Status.FAIL, result.status());
        assertEquals("no PodMonitor selects every broker pod", result.detail());
    }

    /** prometheus-operator reads {any: false} and an empty matchNames as the PodMonitor's own namespace. */
    @Test
    void aPodMonitorThatNamesNoNamespaceWatchesItsOwn() throws IOException {
        podMonitor("kafka", "any-false", Map.of("strimzi.io/cluster", "krafter"), Map.of("any", false));
        podMonitor("monitoring", "no-names", Map.of("strimzi.io/cluster", "krafter"), Map.of("matchNames", List.of()));

        Result result = result(run("{monitoring: {podMonitor: true}}"), "monitoring.podMonitor");

        assertEquals(Status.PASS, result.status());
        assertEquals("kafka/any-false", result.detail());
    }

    @Test
    void withoutThePodMonitorCrdThereIsNone() throws IOException {
        server.expect()
                .get()
                .withPath("/apis/monitoring.coreos.com/v1/podmonitors")
                .andReturn(404, "")
                .always();

        Result result = result(run("{monitoring: {podMonitor: true}}"), "monitoring.podMonitor");

        assertEquals(Status.FAIL, result.status());
        assertEquals("the PodMonitor CRD is not installed", result.detail());
    }

    /** A namespace-scoped role, such as the chart's rbac.scope=namespace, lists the Kafka namespace's only. */
    @Test
    void withoutAccessToOtherNamespacesItReadsTheKafkaNamespacesPodMonitors() throws IOException {
        server.expect()
                .get()
                .withPath("/apis/monitoring.coreos.com/v1/podmonitors")
                .andReturn(403, "")
                .always();
        podMonitor("monitoring", "anywhere", Map.of("strimzi.io/broker-role", "true"), Map.of("any", true));

        Result unseen = result(run("{monitoring: {podMonitor: true}}"), "monitoring.podMonitor");

        assertEquals(Status.UNKNOWN, unseen.status());
        assertEquals(
                "Forbidden: the Kates API's service account may not list PodMonitors outside kafka,"
                        + " and none in kafka selects every broker pod",
                unseen.detail());

        podMonitor("kafka", "brokers", Map.of("strimzi.io/cluster", "krafter"), null);

        Result found = result(run("{monitoring: {podMonitor: true}}"), "monitoring.podMonitor");

        assertEquals(Status.PASS, found.status());
        assertEquals("kafka/brokers", found.detail());
    }

    /** A namespace-scoped role may not list nodes; Strimzi's headless Service lists each Kafka pod's zone. */
    @Test
    void withoutAccessToNodesTheZonesComeFromEndpointSlices() throws IOException {
        place();
        server.expect().get().withPath("/api/v1/nodes").andReturn(403, "").always();
        endpointSlice(Map.of(
                "krafter-controllers-0", "alpha",
                "krafter-controllers-1", "sigma",
                "krafter-controllers-2", "gamma",
                "krafter-brokers-3", "alpha",
                "krafter-brokers-4", "alpha",
                "krafter-brokers-5", "sigma"));

        ConformityReport report =
                run("{controllers: {zones: 3, skew: {max: 0}}, brokers: {zones: {min: 3}, skew: {max: 0}}}");

        assertEquals(Status.PASS, result(report, "controllers.zones").status());
        assertEquals(Status.PASS, result(report, "controllers.skew").status());
        Result zones = result(report, "brokers.zones");
        assertEquals(Status.FAIL, zones.status());
        assertEquals(2, zones.observed());
        assertEquals(
                "alpha: 2, sigma: 1; zones from the EndpointSlices of kafka/krafter-kafka-brokers", zones.detail());
        assertEquals(
                "Kubernetes: pods kafka/strimzi.io/cluster=krafter, the EndpointSlices of kafka/krafter-kafka-brokers",
                zones.source());
        assertEquals(1, result(report, "brokers.skew").observed());
    }

    @Test
    void withoutAccessToNodesOrTheirEndpointsTheZonesAreUnknown() throws IOException {
        server.expect().get().withPath("/api/v1/nodes").andReturn(403, "").always();

        Result zones = result(run("{brokers: {zones: {min: 3}}}"), "brokers.zones");

        assertEquals(Status.UNKNOWN, zones.status());
        assertEquals(
                "Kubernetes: nodes: Forbidden: the Kates API's service account may not list nodes,"
                        + " which are cluster-scoped, and no EndpointSlice of kafka/krafter-kafka-brokers names a node",
                zones.detail());
    }

    /** An endpoint carries only the default zone label, so another one still needs the nodes. */
    @Test
    void withoutAccessToNodesAnotherZoneLabelIsUnknown() throws IOException {
        place();
        server.expect().get().withPath("/api/v1/nodes").andReturn(403, "").always();
        endpointSlice(Map.of("krafter-brokers-3", "alpha"));

        Result zones = result(run("{zoneLabel: example.com/zone, brokers: {zones: {min: 3}}}"), "brokers.zones");

        assertEquals(Status.UNKNOWN, zones.status());
        assertEquals(
                "Kubernetes: nodes: Forbidden: the Kates API's service account may not list nodes,"
                        + " which are cluster-scoped, and only nodes carry example.com/zone,"
                        + " which is not the Kafka CR's spec.kafka.rack.topologyKey",
                zones.detail());
    }

    /**
     * With the zone label as the rack key, Strimzi copies each broker's node label into its rack: brokers 3 and 4
     * on alpha's node, 5 on sigma's, as place() put them. Controller 2's node runs no broker.
     */
    @Test
    @SuppressWarnings("unchecked")
    void withoutAccessToNodesTheRackKeyComesFromTheBrokersRacks() throws IOException {
        place();
        server.expect().get().withPath("/api/v1/nodes").andReturn(403, "").always();
        Map<String, Object> kafkaSpec = new java.util.HashMap<>(KAFKA_SPEC);
        Map<String, Object> kafkaPart =
                new java.util.HashMap<String, Object>((Map<String, Object>) KAFKA_SPEC.get("kafka"));
        kafkaPart.put("rack", Map.of("topologyKey", "example.com/zone"));
        kafkaSpec.put("kafka", kafkaPart);
        client.genericKubernetesResources(ClusterTopologyService.KAFKA_CRD)
                .inNamespace("kafka")
                .withName("krafter")
                .delete();
        create(ClusterTopologyService.KAFKA_CRD, kafka(kafkaSpec, Map.of("clusterId", "4L6g3nShT-eMCtK--X86sw")));
        brokers();
        when(admin.sharedAdminClient().describeCluster().nodes())
                .thenReturn(KafkaFuture.completedFuture(List.of(
                        new Node(3, "b3", 9092, "alpha"),
                        new Node(4, "b4", 9092, "alpha"),
                        new Node(5, "b5", 9092, "sigma"))));

        ConformityReport report = run("{zoneLabel: example.com/zone, controllers: {zones: 3},"
                + " brokers: {zones: {min: 3}, skew: {max: 0}}, kafka: {rackAwareness: true}}");

        Result zones = result(report, "brokers.zones");
        assertEquals(Status.FAIL, zones.status());
        assertEquals(2, zones.observed());
        assertEquals("alpha: 2, sigma: 1; zones from Kafka Admin: describeCluster racks", zones.detail());
        assertEquals(
                "Kubernetes: pods kafka/strimzi.io/cluster=krafter, Kafka Admin: describeCluster racks",
                zones.source());
        assertEquals(1, result(report, "brokers.skew").observed());
        Result controllers = result(report, "controllers.zones");
        assertEquals(Status.UNKNOWN, controllers.status());
        assertEquals(
                "node-gamma runs no broker Kafka reports, and without the node list the zones come from"
                        + " Kafka Admin: describeCluster racks",
                controllers.detail());
        Result rack = result(report, "kafka.rackAwareness");
        assertEquals(Status.PASS, rack.status());
        assertTrue(rack.source().endsWith(".rack, Kafka Admin: describeCluster racks"), rack.source());
    }

    /** Strimzi's headless Service's EndpointSlice: each pod's endpoint on the node place() put it on. */
    void endpointSlice(Map<String, String> zoneByPod) {
        Map<String, String> nodeByZone = Map.of("alpha", "node-alpha", "sigma", "node-sigma", "gamma", "node-gamma");
        List<Endpoint> endpoints = zoneByPod.entrySet().stream()
                .map(e -> new EndpointBuilder()
                        .withAddresses("10.244.0.10")
                        .withNodeName(nodeByZone.get(e.getValue()))
                        .withZone(e.getValue())
                        .withNewTargetRef()
                        .withKind("Pod")
                        .withNamespace("kafka")
                        .withName(e.getKey())
                        .endTargetRef()
                        .build())
                .toList();
        client.discovery()
                .v1()
                .endpointSlices()
                .inNamespace("kafka")
                .resource(new EndpointSliceBuilder()
                        .withNewMetadata()
                        .withName("krafter-kafka-brokers-x1y2z")
                        .withNamespace("kafka")
                        .addToLabels("kubernetes.io/service-name", "krafter-kafka-brokers")
                        .endMetadata()
                        .withAddressType("IPv4")
                        .withEndpoints(endpoints)
                        .build())
                .create();
    }

    @Test
    void namesTheGrantWhenTheKafkaCannotBeRead() {
        server.expect()
                .get()
                .withPath("/apis/kafka.strimzi.io/v1/namespaces/kafka/kafkas/krafter")
                .andReturn(403, "")
                .always();

        ServiceUnavailableException e =
                assertThrows(ServiceUnavailableException.class, () -> run("{brokers: {count: 3}}"));

        assertEquals(
                "The Kates API's service account may not get kafkas.kafka.strimzi.io in kafka: bind it a Role"
                        + " there that grants it, such as the kates chart's with rbac.scope=namespace",
                e.getMessage());
    }

    void podMonitor(String namespace, String name, Map<String, String> matchLabels, Map<String, Object> namespaces) {
        Map<String, Object> spec = new java.util.HashMap<>(Map.of("selector", Map.of("matchLabels", matchLabels)));
        if (namespaces != null) {
            spec.put("namespaceSelector", namespaces);
        }
        client.genericKubernetesResources(ConformityCheck.POD_MONITOR_CRD)
                .inNamespace(namespace)
                .resource(new GenericKubernetesResourceBuilder()
                        .withApiVersion("monitoring.coreos.com/v1")
                        .withKind("PodMonitor")
                        .withNewMetadata()
                        .withName(name)
                        .withNamespace(namespace)
                        .endMetadata()
                        .withAdditionalProperties(Map.of("spec", spec))
                        .build())
                .create();
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
