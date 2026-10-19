package com.bmscomp.kates.conformity;

import static com.bmscomp.kates.conformity.KafkaReferenceExamplesTest.json;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import java.io.IOException;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.List;
import java.util.Map;
import java.util.concurrent.TimeUnit;
import jakarta.ws.rs.ClientErrorException;

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
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.VersionInfo;
import io.fabric8.kubernetes.client.server.mock.EnableKubernetesMockClient;
import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;
import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.Config;
import org.apache.kafka.clients.admin.ConfigEntry;
import org.apache.kafka.clients.admin.DescribeClusterResult;
import org.apache.kafka.clients.admin.DescribeConfigsResult;
import org.apache.kafka.common.KafkaFuture;
import org.apache.kafka.common.Node;
import org.apache.kafka.common.config.ConfigResource;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

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
        return new GenericKubernetesResourceBuilder()
                .withApiVersion("kafka.strimzi.io/v1")
                .withKind("KafkaNodePool")
                .withNewMetadata()
                .withName(name)
                .withNamespace("kafka")
                .addToLabels("strimzi.io/cluster", "krafter")
                .endMetadata()
                .withAdditionalProperties(Map.of(
                        "spec", Map.of("roles", List.of(roles), "replicas", 3, "storage", Map.of("type", storageType))))
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

    /** Brokers 3 and 4 report a rack and min.insync.replicas 2; broker 5 reports neither a rack nor 2. */
    AdminClient brokers() {
        AdminClient kafka = mock(AdminClient.class);
        when(admin.sharedAdminClient()).thenReturn(kafka);
        DescribeClusterResult cluster = mock(DescribeClusterResult.class);
        when(cluster.nodes())
                .thenReturn(KafkaFuture.completedFuture(List.of(
                        new Node(3, "b3", 9092, "alpha"), new Node(4, "b4", 9092, "sigma"), new Node(5, "b5", 9092))));
        when(kafka.describeCluster()).thenReturn(cluster);
        Map<ConfigResource, Config> configs = new java.util.LinkedHashMap<>();
        for (int id = 3; id <= 5; id++) {
            configs.put(
                    new ConfigResource(ConfigResource.Type.BROKER, String.valueOf(id)),
                    new Config(List.of(
                            new ConfigEntry("min.insync.replicas", id == 5 ? "1" : "2"),
                            new ConfigEntry(
                                    "ssl.keystore.password",
                                    null,
                                    ConfigEntry.ConfigSource.STATIC_BROKER_CONFIG,
                                    true,
                                    false,
                                    List.of(),
                                    ConfigEntry.ConfigType.PASSWORD,
                                    null))));
        }
        DescribeConfigsResult described = mock(DescribeConfigsResult.class);
        when(described.all()).thenReturn(KafkaFuture.completedFuture(configs));
        when(kafka.describeConfigs(org.mockito.ArgumentMatchers.anyCollection()))
                .thenReturn(described);
        return kafka;
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
        org.apache.kafka.common.internals.KafkaFutureImpl<java.util.Collection<Node>> failed =
                new org.apache.kafka.common.internals.KafkaFutureImpl<>();
        failed.completeExceptionally(new org.apache.kafka.common.errors.TimeoutException("Timed out waiting"));
        when(cluster.nodes()).thenReturn(failed);
        when(kafka.describeCluster()).thenReturn(cluster);

        ConformityReport report = run("{kafka: {source: brokers, config: {min.insync.replicas: {min: 2}}}}");

        Result result = report.results().get(0);
        assertEquals(Status.UNKNOWN, result.status());
        assertTrue(result.detail().contains("Timed out waiting"), result.detail());
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
