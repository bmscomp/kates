package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.time.Instant;
import java.util.Map;

import io.fabric8.kubernetes.api.model.networking.v1.NetworkPolicy;
import io.fabric8.kubernetes.api.model.networking.v1.NetworkPolicyBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.server.mock.EnableKubernetesMockClient;
import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.ScaleDownSnapshots;
import com.bmscomp.kates.chaos.StrimziTestCluster;
import com.bmscomp.kates.chaos.VertxPerMockClient;

/**
 * Startup recovery of the faults a plan left when it died with the Kates pod.
 * The scale-down recovery used to look only at StatefulSets, which Strimzi does
 * not create, and every recovery used to skip a fault less than 15 minutes old,
 * which is every fault a normal restart leaves.
 */
@EnableKubernetesMockClient(crud = true, kubernetesClientBuilderCustomizer = VertxPerMockClient.class)
class DisruptionOrphanReconcilerTest {

    KubernetesMockServer server;
    KubernetesClient client;

    @Test
    void restoresANodePoolAbandonedMidPlanAndLeavesARecentScaleDownAlone() {
        Instant startedAt = Instant.now();
        StrimziTestCluster cluster = new StrimziTestCluster(server, client)
                .pool("brokers-alpha", "broker", 3)
                .pool("brokers-sigma", "broker", 4)
                .pool("brokers-gamma", "broker", 5)
                .pool("brokers-delta", "broker", 6);
        cluster.scaledDown("brokers-sigma", 0);
        // A plan this process started.
        cluster.scaledDown("brokers-gamma", 0, startedAt.plusSeconds(1).toEpochMilli());
        // The pod was killed 2 minutes into the plan and came back 90 s later.
        cluster.scaledDown("brokers-delta", 0, startedAt.minusSeconds(120).toEpochMilli());

        ScaleDownSnapshots.Restored restored = reconciler().reconcileScaleDowns(startedAt);

        assertEquals(new ScaleDownSnapshots.Restored(0, 2), restored);
        assertEquals(1, cluster.replicas("brokers-sigma"));
        assertTrue(cluster.annotations("brokers-sigma").isEmpty(), "snapshot cleared");
        assertEquals(1, cluster.replicas("brokers-delta"));
        assertEquals(0, cluster.replicas("brokers-gamma"));
        assertEquals(1, cluster.replicas("brokers-alpha"));
    }

    @Test
    void deletesKatesNetworkPoliciesFromBeforeTheStartInEveryNamespace() {
        // The mock server stamps creationTimestamp itself, so the start moves
        // around the policies instead.
        policy("kafka", "kates-netpol-krafter-brokers-sigma-4", true);
        policy("kates", "kates-netpol-consumer", true);
        policy("kafka", "allow-brokers", false);
        DisruptionOrphanReconciler reconciler = reconciler();

        assertEquals(0, reconciler.reconcileNetworkPolicies(Instant.now().minusSeconds(60)));
        assertNotNull(get("kafka", "kates-netpol-krafter-brokers-sigma-4"), "made after the start");

        assertEquals(2, reconciler.reconcileNetworkPolicies(Instant.now().plusSeconds(60)));
        assertNull(get("kafka", "kates-netpol-krafter-brokers-sigma-4"));
        assertNull(get("kates", "kates-netpol-consumer"));
        assertNotNull(get("kafka", "allow-brokers"), "not Kates-managed");
    }

    @Test
    void aPolicyStampedInTheSecondTheProcessStartedIsKept() {
        NetworkPolicy created = policy("kafka", "kates-netpol-krafter-brokers-sigma-4", true);
        // Stamped 12:00:00Z by a plan this process launched at 12:00:00.700.
        Instant startedAt =
                Instant.parse(created.getMetadata().getCreationTimestamp()).plusMillis(700);

        assertEquals(0, reconciler().reconcileNetworkPolicies(startedAt));
        assertNotNull(get("kafka", "kates-netpol-krafter-brokers-sigma-4"));
    }

    @Test
    void uncordonsANodeADrainLeftCordonedAndLeavesTheOthers() {
        Instant startedAt = Instant.now();
        cordoned("node-a", startedAt.minusSeconds(120), Instant.now().plusSeconds(3600));
        // A drain this process started.
        cordoned("node-b", startedAt.plusSeconds(1), Instant.now().plusSeconds(3600));
        // Past its deadline, found by a later pass.
        cordoned("node-c", startedAt.plusSeconds(1), Instant.now().minusSeconds(1));
        // Cordoned by someone else.
        client.nodes()
                .resource(new io.fabric8.kubernetes.api.model.NodeBuilder()
                        .withNewMetadata()
                        .withName("node-d")
                        .endMetadata()
                        .withNewSpec()
                        .withUnschedulable(true)
                        .endSpec()
                        .build())
                .create();

        assertEquals(2, reconciler().reconcileCordons(startedAt));

        assertNull(client.nodes().withName("node-a").get().getSpec().getUnschedulable());
        assertTrue(
                client.nodes()
                        .withName("node-a")
                        .get()
                        .getMetadata()
                        .getLabels()
                        .isEmpty(),
                "label taken off");
        assertEquals(
                Boolean.TRUE, client.nodes().withName("node-b").get().getSpec().getUnschedulable());
        assertNull(client.nodes().withName("node-c").get().getSpec().getUnschedulable());
        assertEquals(
                Boolean.TRUE, client.nodes().withName("node-d").get().getSpec().getUnschedulable());
    }

    private void cordoned(String name, Instant at, Instant deadline) {
        client.nodes()
                .resource(new io.fabric8.kubernetes.api.model.NodeBuilder()
                        .withNewMetadata()
                        .withName(name)
                        .addToLabels(com.bmscomp.kates.chaos.KubernetesChaosProvider.CORDONED_LABEL, "true")
                        .addToAnnotations(
                                com.bmscomp.kates.chaos.KubernetesChaosProvider.CORDONED_AT_ANNOTATION,
                                String.valueOf(at.toEpochMilli()))
                        .addToAnnotations(com.bmscomp.kates.chaos.FaultMarks.DEADLINE, deadline.toString())
                        .endMetadata()
                        .withNewSpec()
                        .withUnschedulable(true)
                        .endSpec()
                        .build())
                .create();
    }

    private DisruptionOrphanReconciler reconciler() {
        DisruptionOrphanReconciler reconciler = new DisruptionOrphanReconciler();
        reconciler.kubeClient = client;
        reconciler.kafkaNamespace = StrimziTestCluster.NAMESPACE;
        reconciler.minAgeSec = 0;
        return reconciler;
    }

    private NetworkPolicy policy(String namespace, String name, boolean katesManaged) {
        return client.network()
                .networkPolicies()
                .inNamespace(namespace)
                .resource(new NetworkPolicyBuilder()
                        .withNewMetadata()
                        .withName(name)
                        .withNamespace(namespace)
                        .withLabels(katesManaged ? Map.of("managed-by", "kates") : Map.of())
                        .endMetadata()
                        .build())
                .create();
    }

    private NetworkPolicy get(String namespace, String name) {
        return client.network()
                .networkPolicies()
                .inNamespace(namespace)
                .withName(name)
                .get();
    }

    // ---- J4b: by term and deadline, every pass ----

    private static final Instant NOW = Instant.parse("2026-11-15T12:00:00Z");

    private com.bmscomp.kates.quorum.Quorum quorumAt(long term, boolean leads) {
        com.bmscomp.kates.quorum.Quorum quorum = org.mockito.Mockito.mock(com.bmscomp.kates.quorum.Quorum.class);
        org.mockito.Mockito.when(quorum.term()).thenReturn(term);
        org.mockito.Mockito.when(quorum.memberId()).thenReturn("m0");
        org.mockito.Mockito.when(quorum.acceptsWrites()).thenReturn(leads);
        return quorum;
    }

    private static io.fabric8.kubernetes.api.model.ObjectMeta meta(String term, Instant deadline, Instant created) {
        Map<String, String> a = new java.util.HashMap<>();
        if (term != null) {
            a.put(com.bmscomp.kates.chaos.FaultMarks.TERM, term);
        }
        if (deadline != null) {
            a.put(com.bmscomp.kates.chaos.FaultMarks.DEADLINE, deadline.toString());
        }
        return new io.fabric8.kubernetes.api.model.ObjectMetaBuilder()
                .withAnnotations(a)
                .withCreationTimestamp(created.toString())
                .build();
    }

    @org.junit.jupiter.params.ParameterizedTest
    @org.junit.jupiter.params.provider.CsvSource(
            nullValues = "-",
            value = {
                // term, deadline from NOW (s), created from cutoff (s), current term, in a quorum, age pass, stray
                "3, -, 10, 4, true, false, true",
                "4, -, 10, 4, true, false, false",
                "4, -10, 10, 4, true, false, true",
                "4, 3600, 10, 4, true, false, false",
                "-, -, -10, 4, true, true, true",
                "-, -, 10, 4, true, true, false",
                "-, -, -10, 4, true, false, false",
                "0, -, -10, 0, false, true, true",
                "0, -, 10, 0, false, true, false",
                "0, -, -10, 0, false, false, false",
            })
    void strayRule(
            String term,
            Long deadlineIn,
            long createdIn,
            long current,
            boolean inQuorum,
            boolean agePass,
            boolean stray) {
        DisruptionOrphanReconciler reconciler = reconciler();
        reconciler.quorum = quorumAt(current, true);
        if (!inQuorum) {
            org.mockito.Mockito.when(reconciler.quorum.memberId()).thenReturn(null);
        }
        Instant cutoff = NOW.minusSeconds(60);
        var meta = meta(term, deadlineIn == null ? null : NOW.plusSeconds(deadlineIn), cutoff.plusSeconds(createdIn));

        assertEquals(stray, reconciler.isStray(meta, agePass ? cutoff.toEpochMilli() : null, NOW));
    }

    private NetworkPolicy marked(String namespace, String name, String term) {
        return client.network()
                .networkPolicies()
                .inNamespace(namespace)
                .resource(new NetworkPolicyBuilder()
                        .withNewMetadata()
                        .withName(name)
                        .withNamespace(namespace)
                        .withLabels(Map.of("managed-by", "kates"))
                        .withAnnotations(Map.of(
                                com.bmscomp.kates.chaos.FaultMarks.TERM,
                                term,
                                com.bmscomp.kates.chaos.FaultMarks.DEADLINE,
                                Instant.now().plusSeconds(3600).toString()))
                        .endMetadata()
                        .build())
                .create();
    }

    private DisruptionOrphanReconciler leaderAt(long term) {
        DisruptionOrphanReconciler reconciler = reconciler();
        reconciler.enabled = true;
        reconciler.quorum = quorumAt(term, true);
        reconciler.repository = org.mockito.Mockito.mock(DisruptionReportRepository.class);
        reconciler.since = Instant.now().minusSeconds(60);
        return reconciler;
    }

    @Test
    void theCurrentTermsFaultIsKept() {
        marked("kafka", "kates-netpol-a", "4");

        leaderAt(4).periodic();

        assertNotNull(get("kafka", "kates-netpol-a"));
    }

    @Test
    void anOlderTermsPartitionGoesOnTheNextPass() {
        // A paused leader's late write: made after this member took over, with the old term.
        marked("kafka", "kates-netpol-late", "3");

        leaderAt(4).periodic();

        assertNull(get("kafka", "kates-netpol-late"));
    }

    @Test
    void anOlderTermsEngineGoes() {
        client.nodes()
                .resource(new io.fabric8.kubernetes.api.model.NodeBuilder()
                        .withNewMetadata()
                        .withName("n1")
                        .endMetadata()
                        .withNewSpec()
                        .withUnschedulable(true)
                        .endSpec()
                        .build())
                .create();
        com.bmscomp.kates.chaos.litmus.ChaosEngine engine = new com.bmscomp.kates.chaos.litmus.ChaosEngine();
        engine.getMetadata().setName("kates-drain-1");
        engine.getMetadata().setNamespace("kafka");
        engine.getMetadata().setLabels(Map.of("managed-by", "kates"));
        engine.getMetadata().setAnnotations(Map.of(com.bmscomp.kates.chaos.FaultMarks.TERM, "3"));
        var spec = new com.bmscomp.kates.chaos.litmus.ChaosEngineSpec();
        var experiment = new com.bmscomp.kates.chaos.litmus.ChaosEngineSpec.Experiment();
        experiment.name = "node-drain";
        experiment.spec = new com.bmscomp.kates.chaos.litmus.ChaosEngineSpec.ExperimentSpec();
        experiment.spec.components = new com.bmscomp.kates.chaos.litmus.ChaosEngineSpec.Components();
        experiment.spec.components.env =
                java.util.List.of(new com.bmscomp.kates.chaos.litmus.ChaosEngineSpec.EnvVar("TARGET_NODE", "n1"));
        spec.experiments = java.util.List.of(experiment);
        engine.setSpec(spec);
        client.resources(com.bmscomp.kates.chaos.litmus.ChaosEngine.class)
                .inNamespace("kafka")
                .resource(engine)
                .create();

        leaderAt(4).periodic();

        assertEquals(
                java.util.List.of(),
                client.resources(com.bmscomp.kates.chaos.litmus.ChaosEngine.class)
                        .inAnyNamespace()
                        .list()
                        .getItems());
    }

    @Test
    void aScaleDownOutsideTheKafkaNamespaceIsRestored() {
        client.apps()
                .statefulSets()
                .inNamespace("other")
                .resource(new io.fabric8.kubernetes.api.model.apps.StatefulSetBuilder()
                        .withNewMetadata()
                        .withName("victim")
                        .withNamespace("other")
                        .withAnnotations(Map.of(
                                com.bmscomp.kates.chaos.KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION, "3",
                                com.bmscomp.kates.chaos.FaultMarks.TERM, "3"))
                        .endMetadata()
                        .withNewSpec()
                        .withReplicas(2)
                        .endSpec()
                        .build())
                .create();

        leaderAt(4).periodic();

        assertEquals(
                3,
                client.apps()
                        .statefulSets()
                        .inNamespace("other")
                        .withName("victim")
                        .get()
                        .getSpec()
                        .getReplicas());
    }

    @Test
    void aFailedUndoLeavesReportsRunning() {
        marked("kafka", "kates-netpol-stuck", "3");
        server.expect()
                .delete()
                .withPath("/apis/networking.k8s.io/v1/namespaces/kafka/networkpolicies/kates-netpol-stuck")
                .andReturn(403, "forbidden")
                .once();
        DisruptionOrphanReconciler reconciler = leaderAt(4);

        reconciler.periodic();
        org.mockito.Mockito.verify(reconciler.repository, org.mockito.Mockito.never())
                .findByStatusCreatedBefore(
                        org.mockito.ArgumentMatchers.anyString(), org.mockito.ArgumentMatchers.any());

        reconciler.periodic();
        assertNull(get("kafka", "kates-netpol-stuck"));
        org.mockito.Mockito.verify(reconciler.repository)
                .findByStatusCreatedBefore(
                        org.mockito.ArgumentMatchers.eq("RUNNING"), org.mockito.ArgumentMatchers.any());
    }

    @Test
    void aFollowerReconcilesNothing() {
        marked("kafka", "kates-netpol-old", "3");
        DisruptionOrphanReconciler reconciler = leaderAt(4);
        org.mockito.Mockito.when(reconciler.quorum.acceptsWrites()).thenReturn(false);
        int before = server.getRequestCount();

        reconciler.periodic();

        assertEquals(before, server.getRequestCount(), "a follower asks the API nothing");
        assertNotNull(get("kafka", "kates-netpol-old"));
    }

    @Test
    void aLoneInstanceKeepsItsOwnPartitionOnEveryPass() {
        marked("kafka", "kates-netpol-mine", "0");
        DisruptionOrphanReconciler reconciler = leaderAt(0);
        org.mockito.Mockito.when(reconciler.quorum.memberId()).thenReturn(null);

        reconciler.periodic();

        assertNotNull(
                get("kafka", "kates-netpol-mine"), "the periodic pass of a lone instance judges by deadline only");
    }

    @Test
    void aFailedListLeavesReportsRunning() {
        // The Kubernetes API refuses the list: whether a partition is still in is unknown.
        server.expect()
                .get()
                .withPath("/apis/networking.k8s.io/v1/networkpolicies?labelSelector=managed-by%3Dkates")
                .andReturn(403, "forbidden")
                .once();
        DisruptionOrphanReconciler reconciler = leaderAt(4);

        reconciler.periodic();

        org.mockito.Mockito.verify(reconciler.repository, org.mockito.Mockito.never())
                .findByStatusCreatedBefore(
                        org.mockito.ArgumentMatchers.anyString(), org.mockito.ArgumentMatchers.any());
    }

    @Test
    void aPassRemovesFaultsBeforeAskingTheDatabase() {
        marked("kafka", "kates-netpol-late", "3");
        DisruptionOrphanReconciler reconciler = leaderAt(4);
        reconciler.orchestrator = org.mockito.Mockito.mock(com.bmscomp.kates.engine.TestOrchestrator.class);
        java.util.concurrent.atomic.AtomicBoolean goneFirst = new java.util.concurrent.atomic.AtomicBoolean();
        org.mockito.Mockito.doAnswer(invocation -> {
                    goneFirst.set(get("kafka", "kates-netpol-late") == null);
                    return null;
                })
                .when(reconciler.orchestrator)
                .recoverPreviousLeaders();

        reconciler.periodic();

        assertTrue(goneFirst.get(), "a database that hangs must not hold the partition");
    }
}
