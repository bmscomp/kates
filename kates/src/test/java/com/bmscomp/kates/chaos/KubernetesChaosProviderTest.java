package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.concurrent.atomic.AtomicBoolean;

import io.fabric8.kubernetes.api.model.Pod;
import io.fabric8.kubernetes.api.model.PodBuilder;
import io.fabric8.kubernetes.api.model.apps.StatefulSet;
import io.fabric8.kubernetes.api.model.apps.StatefulSetBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.server.mock.EnableKubernetesMockClient;
import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;
import io.fabric8.mockwebserver.http.RecordedRequest;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

@EnableKubernetesMockClient(crud = true, kubernetesClientBuilderCustomizer = VertxPerMockClient.class)
public class KubernetesChaosProviderTest {

    KubernetesMockServer server;
    KubernetesClient client;

    private KubernetesChaosProvider provider;

    @BeforeEach
    void setup() {
        provider = new KubernetesChaosProvider();
        provider.client = client;
        provider.self = provider;
        // triggerFault() now runs on the shared managed executor (P3-3);
        // this hand-built provider isn't CDI-managed, so wire one directly.
        provider.executor = new com.bmscomp.kates.engine.KatesExecutor();
    }

    @Test
    void testPodKill() throws ExecutionException, InterruptedException, TimeoutException {
        // Create a pod in the mock server
        Pod pod = new PodBuilder()
                .withNewMetadata()
                .withName("test-pod")
                .withNamespace("default")
                .addToLabels("app", "test")
                .endMetadata()
                .withNewSpec()
                .addNewContainer()
                .withName("nginx")
                .withImage("nginx")
                .endContainer()
                .endSpec()
                .build();
        client.pods().inNamespace("default").resource(pod).create();

        FaultSpec spec = FaultSpec.builder("test-pod-kill")
                .targetNamespace("default")
                .targetLabel("app=test")
                .disruptionType(DisruptionType.POD_KILL)
                .build();

        ChaosOutcome outcome = provider.triggerFault(spec).get(5, TimeUnit.SECONDS);

        assertNotNull(outcome);
        assertTrue(outcome.isPass());
        assertNull(outcome.failureReason());

        // Verify pod is deleted
        Pod deletedPod =
                client.pods().inNamespace("default").withName("test-pod").get();
        assertNull(deletedPod);
    }

    @Test
    void testCpuStress() throws ExecutionException, InterruptedException, TimeoutException {
        Pod pod = new PodBuilder()
                .withNewMetadata()
                .withName("broker-0")
                .withNamespace("default")
                .addToLabels("app", "kafka")
                .endMetadata()
                .withNewSpec()
                .addNewContainer()
                .withName("kafka")
                .withImage("kafka")
                .endContainer()
                .endSpec()
                .build();
        client.pods().inNamespace("default").resource(pod).create();

        // Ephemeral containers can only be written through the subresource, which the
        // CRUD mock does not serve — and it would accept the plain pod update a real
        // API server rejects. Answer the write here and assert on what was sent.
        String subresource = "/api/v1/namespaces/default/pods/broker-0/ephemeralcontainers";
        server.expect().put().withPath(subresource).andReturn(200, pod).once();

        FaultSpec spec = FaultSpec.builder("test-cpu-stress")
                .targetNamespace("default")
                .targetLabel("app=kafka")
                .disruptionType(DisruptionType.CPU_STRESS)
                .cpuCores(2)
                .chaosDurationSec(10)
                .build();

        ChaosOutcome outcome = provider.triggerFault(spec).get(5, TimeUnit.SECONDS);

        assertNotNull(outcome);
        assertTrue(outcome.isPass());

        RecordedRequest write = server.getLastRequest();
        assertEquals("PUT", write.getMethod());
        assertEquals(subresource, write.getPath());
        Pod sent = client.getKubernetesSerialization().unmarshal(write.getUtf8Body(), Pod.class);
        assertEquals(1, sent.getSpec().getEphemeralContainers().size());
        assertEquals(
                "chaos-cpu-stress",
                sent.getSpec().getEphemeralContainers().get(0).getName());
    }

    @Test
    void testIoStress() throws ExecutionException, InterruptedException, TimeoutException {
        Pod pod = new PodBuilder()
                .withNewMetadata()
                .withName("broker-1")
                .withNamespace("default")
                .addToLabels("app", "kafka2")
                .endMetadata()
                .withNewSpec()
                .addNewContainer()
                .withName("kafka")
                .withImage("kafka")
                .endContainer()
                .endSpec()
                .build();
        client.pods().inNamespace("default").resource(pod).create();

        String subresource = "/api/v1/namespaces/default/pods/broker-1/ephemeralcontainers";
        server.expect().put().withPath(subresource).andReturn(200, pod).once();

        FaultSpec spec = FaultSpec.builder("test-io-stress")
                .targetNamespace("default")
                .targetLabel("app=kafka2")
                .disruptionType(DisruptionType.IO_STRESS)
                .ioWorkers(4)
                .chaosDurationSec(10)
                .build();

        ChaosOutcome outcome = provider.triggerFault(spec).get(5, TimeUnit.SECONDS);

        assertNotNull(outcome);
        assertTrue(outcome.isPass());

        RecordedRequest write = server.getLastRequest();
        assertEquals("PUT", write.getMethod());
        assertEquals(subresource, write.getPath());
        Pod sent = client.getKubernetesSerialization().unmarshal(write.getUtf8Body(), Pod.class);
        assertEquals(1, sent.getSpec().getEphemeralContainers().size());
        assertEquals(
                "chaos-io-stress",
                sent.getSpec().getEphemeralContainers().get(0).getName());
    }

    @Test
    void testScaleDownSnapshotsOriginalReplicas() throws ExecutionException, InterruptedException, TimeoutException {
        // A Kafka not run by Strimzi: pods of a StatefulSet.
        StatefulSet ss = new StatefulSetBuilder()
                .withNewMetadata()
                .withName("kafka")
                .withNamespace("default")
                .addToLabels("app", "kafka")
                .endMetadata()
                .withNewSpec()
                .withReplicas(3)
                .endSpec()
                .build();
        client.apps().statefulSets().inNamespace("default").resource(ss).create();
        for (int i = 0; i < 3; i++) {
            client.pods()
                    .inNamespace("default")
                    .resource(new PodBuilder()
                            .withNewMetadata()
                            .withName("kafka-" + i)
                            .withNamespace("default")
                            .addToLabels("app", "kafka")
                            .addNewOwnerReference()
                            .withApiVersion("apps/v1")
                            .withKind("StatefulSet")
                            .withName("kafka")
                            .withUid("sts-uid")
                            .endOwnerReference()
                            .endMetadata()
                            .build())
                    .create();
        }

        FaultSpec spec = FaultSpec.builder("scale-down")
                .targetNamespace("default")
                .targetLabel("app=kafka")
                .disruptionType(DisruptionType.SCALE_DOWN)
                .build();

        // First SCALE_DOWN: 3 → 2, and the ORIGINAL count (3) is snapshotted.
        assertTrue(provider.triggerFault(spec).get(5, TimeUnit.SECONDS).isPass());
        StatefulSet after = client.apps()
                .statefulSets()
                .inNamespace("default")
                .withName("kafka")
                .get();
        assertEquals(2, after.getSpec().getReplicas(), "one replica removed");
        assertEquals(
                "3",
                after.getMetadata().getAnnotations().get(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION),
                "original replica count snapshotted for rollback");

        // Second SCALE_DOWN: 2 → 1, snapshot must NOT be overwritten with 2.
        assertTrue(provider.triggerFault(spec).get(5, TimeUnit.SECONDS).isPass());
        StatefulSet after2 = client.apps()
                .statefulSets()
                .inNamespace("default")
                .withName("kafka")
                .get();
        assertEquals(1, after2.getSpec().getReplicas());
        assertEquals(
                "3",
                after2.getMetadata().getAnnotations().get(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION),
                "snapshot preserved across repeated scale-downs");

        // Third: the last replica stays, and the step says so instead of passing.
        ChaosOutcome third = provider.triggerFault(spec).get(5, TimeUnit.SECONDS);
        assertFalse(third.isPass());
        assertTrue(third.failureReason().contains("one replica"), third.failureReason());
    }

    // ── SCALE_DOWN on Strimzi: KafkaNodePools ──────────────────────────────

    private static FaultSpec scaleDown(String selector, int chaosDurationSec) {
        return FaultSpec.builder("scale-down")
                .targetLabel(selector)
                .disruptionType(DisruptionType.SCALE_DOWN)
                .chaosDurationSec(chaosDurationSec)
                .build();
    }

    private StrimziTestCluster strimzi() {
        provider.scalePollIntervalMs = 20;
        return new StrimziTestCluster(server, client)
                .pool("controllers", "controller", 0, 1, 2)
                .pool("brokers", "broker", 3, 4, 5);
    }

    @Test
    void scaleDownLowersTheNodePoolAndWaitsForTheOperatorToRemoveTheBroker() throws Exception {
        StrimziTestCluster cluster = strimzi().kafka(false);

        // Used to look for StatefulSets, which Strimzi does not create, and
        // "succeed" having scaled nothing.
        try (var operator = cluster.operator(pool -> true)) {
            ChaosOutcome outcome = provider.triggerFault(scaleDown("strimzi.io/pool-name=brokers", 10))
                    .get(15, TimeUnit.SECONDS);

            assertTrue(outcome.isPass(), outcome.failureReason());
            assertFalse(
                    cluster.pods().contains("krafter-brokers-5"),
                    "the step returned once the broker with the highest node ID was gone");
        }
        assertEquals(2, cluster.replicas("brokers"));
        assertEquals("3", cluster.annotations("brokers").get(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION));
        assertTrue(cluster.annotations("brokers").containsKey(KubernetesChaosProvider.SCALED_DOWN_AT_ANNOTATION));
    }

    @Test
    void scaleDownTakesTheNodePoolsOfTheBrokersTheSelectorMatchesAndLeavesTheControllers() throws Exception {
        StrimziTestCluster cluster = strimzi().pool("brokers-sigma", "broker", 6, 7);

        ChaosOutcome outcome = provider.triggerFault(scaleDown("strimzi.io/component-type=kafka", 0))
                .get(5, TimeUnit.SECONDS);

        assertTrue(outcome.isPass(), outcome.failureReason());
        assertEquals(2, cluster.replicas("brokers"));
        assertEquals(1, cluster.replicas("brokers-sigma"), "one broker from each pool, not one in all");
        assertEquals(3, cluster.replicas("controllers"), "Strimzi scales down broker-only pools only");
    }

    @Test
    void scaleDownTheOperatorHoldsBackFailsAtOnceAndGivesTheReplicasBack() throws Exception {
        StrimziTestCluster cluster = strimzi().kafka(false);

        // The brokers still host partition replicas and nothing moves them off.
        try (var operator = cluster.operator(pool -> false)) {
            ChaosOutcome outcome = provider.triggerFault(scaleDown("strimzi.io/pool-name=brokers", 60))
                    .get(5, TimeUnit.SECONDS);

            assertFalse(outcome.isPass());
            assertTrue(outcome.failureReason().contains("held back"), outcome.failureReason());
            assertTrue(outcome.failureReason().contains("skip-broker-scaledown-check"), outcome.failureReason());
        }
        // Left lowered, the scale-down would go through whenever the broker empties.
        assertEquals(3, cluster.replicas("brokers"));
        assertFalse(cluster.annotations("brokers").containsKey(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION));
        assertTrue(cluster.pods().contains("krafter-brokers-5"));
    }

    @Test
    void scaleDownWaitsWhileCruiseControlDrainsTheBroker() throws Exception {
        StrimziTestCluster cluster = strimzi().kafka(true);
        long drainedAt = System.nanoTime() + 300_000_000L;

        // Held back at first, as Strimzi does while the remove-brokers
        // auto-rebalance moves the replicas off; removed once they are.
        try (var operator = cluster.operator(pool -> System.nanoTime() > drainedAt)) {
            ChaosOutcome outcome = provider.triggerFault(scaleDown("strimzi.io/pool-name=brokers", 10))
                    .get(15, TimeUnit.SECONDS);

            assertTrue(outcome.isPass(), outcome.failureReason());
        }
        assertEquals(2, cluster.replicas("brokers"));
        assertFalse(cluster.pods().contains("krafter-brokers-5"));
    }

    @Test
    void scaleDownNotFinishedInTimeFailsAndGivesTheReplicasBack() throws Exception {
        StrimziTestCluster cluster = strimzi().kafka(true);

        // No Cluster Operator reconciles the change.
        ChaosOutcome outcome = provider.triggerFault(scaleDown("strimzi.io/pool-name=brokers", 1))
                .get(5, TimeUnit.SECONDS);

        assertFalse(outcome.isPass());
        assertTrue(
                outcome.failureReason().contains("not finished within chaosDurationSec=1s"), outcome.failureReason());
        assertEquals(3, cluster.replicas("brokers"));
        assertFalse(cluster.annotations("brokers").containsKey(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION));
    }

    @Test
    void secondScaleDownKeepsTheOriginalSnapshot() throws Exception {
        StrimziTestCluster cluster = strimzi().kafka(false);

        try (var operator = cluster.operator(pool -> true)) {
            FaultSpec spec = scaleDown("strimzi.io/pool-name=brokers", 10);
            assertTrue(provider.triggerFault(spec).get(15, TimeUnit.SECONDS).isPass());
            assertTrue(provider.triggerFault(spec).get(15, TimeUnit.SECONDS).isPass());
        }
        assertEquals(1, cluster.replicas("brokers"));
        assertEquals(
                "3",
                cluster.annotations("brokers").get(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION),
                "rollback restores the count before the first step, not the second");
    }

    @Test
    void scaleDownDoesNotAddToAChangeTheOperatorHasNotFinished() throws Exception {
        StrimziTestCluster cluster = strimzi();
        FaultSpec spec = scaleDown("strimzi.io/pool-name=brokers", 0);
        assertTrue(provider.triggerFault(spec).get(5, TimeUnit.SECONDS).isPass());

        // No operator ran: the pool still lists three nodes for two replicas.
        ChaosOutcome second = provider.triggerFault(spec).get(5, TimeUnit.SECONDS);

        assertFalse(second.isPass());
        assertTrue(second.failureReason().contains("already changing size"), second.failureReason());
        assertEquals(2, cluster.replicas("brokers"));
    }

    @Test
    void scaleDownRefusesToRemoveTheLastBrokerOfTheCluster() throws Exception {
        StrimziTestCluster cluster = new StrimziTestCluster(server, client)
                .pool("controllers", "controller", 0, 1, 2)
                .pool("brokers-alpha", "broker", 3)
                .pool("brokers-sigma", "broker", 4);

        ChaosOutcome outcome = provider.triggerFault(scaleDown("strimzi.io/broker-role=true", 0))
                .get(5, TimeUnit.SECONDS);

        assertFalse(outcome.isPass());
        assertTrue(outcome.failureReason().contains("last broker of Kafka cluster krafter"), outcome.failureReason());
        assertEquals(1, cluster.replicas("brokers-alpha"));
        assertEquals(1, cluster.replicas("brokers-sigma"));
    }

    @Test
    void scaleDownOfAControllerPoolFails() throws Exception {
        StrimziTestCluster cluster = strimzi();

        ChaosOutcome outcome = provider.triggerFault(scaleDown("strimzi.io/pool-name=controllers", 0))
                .get(5, TimeUnit.SECONDS);

        assertFalse(outcome.isPass());
        assertTrue(outcome.failureReason().contains("runs KRaft controllers"), outcome.failureReason());
        assertEquals(3, cluster.replicas("controllers"));
    }

    private void createZonedBrokers() {
        String[][] brokers = {
            {"krafter-brokers-0", "alpha"}, {"krafter-brokers-1", "alpha"}, {"krafter-brokers-2", "sigma"}
        };
        for (String[] b : brokers) {
            client.pods().inNamespace("kafka").resource(broker(b[0], b[1])).create();
        }
    }

    /** A broker pod as Strimzi runs it: owned by a StrimziPodSet. The mock server assigns its UID. */
    private static Pod broker(String name, String zone) {
        return new PodBuilder()
                .withNewMetadata()
                .withName(name)
                .withNamespace("kafka")
                .addToLabels("strimzi.io/component-type", "kafka")
                .addToLabels("zone", zone)
                .addNewOwnerReference()
                .withApiVersion("core.strimzi.io/v1")
                .withKind("StrimziPodSet")
                .withName("krafter-brokers")
                .withUid("podset-uid")
                .endOwnerReference()
                .endMetadata()
                .withNewStatus()
                .withPhase("Running")
                .addNewCondition()
                .withType("Ready")
                .withStatus("True")
                .endCondition()
                .endStatus()
                .build();
    }

    private List<String> uids() {
        return client.pods().inNamespace("kafka").list().getItems().stream()
                .map(p -> p.getMetadata().getUid())
                .toList();
    }

    private boolean annotatedForRoll(String podName) {
        var annotations = client.pods()
                .inNamespace("kafka")
                .withName(podName)
                .get()
                .getMetadata()
                .getAnnotations();
        return annotations != null
                && "true".equals(annotations.get(KubernetesChaosProvider.MANUAL_ROLLING_UPDATE_ANNOTATION));
    }

    private static FaultSpec rollingRestart(String selector, int chaosDurationSec) {
        return FaultSpec.builder("rolling-restart")
                .targetLabel(selector)
                .disruptionType(DisruptionType.ROLLING_RESTART)
                .chaosDurationSec(chaosDurationSec)
                .build();
    }

    @Test
    void rollingRestartAnnotatesEveryStrimziPodTheSelectorMatches() throws Exception {
        createZonedBrokers();

        // Used to look for StatefulSets, which Strimzi does not create, and
        // "succeed" having restarted nothing.
        ChaosOutcome outcome = provider.triggerFault(rollingRestart("strimzi.io/component-type=kafka,zone=alpha", 0))
                .get(5, TimeUnit.SECONDS);

        assertTrue(outcome.isPass(), outcome.failureReason());
        assertTrue(annotatedForRoll("krafter-brokers-0"));
        assertTrue(annotatedForRoll("krafter-brokers-1"), "every matching pod, not one at random");
        assertFalse(annotatedForRoll("krafter-brokers-2"));
    }

    @Test
    void rollingRestartWaitsForTheOperatorToReplaceEveryPod() throws Exception {
        createZonedBrokers();
        provider.rollPollIntervalMs = 20;
        List<String> before = uids();

        // Stands in for the Cluster Operator: replaces each annotated pod, one
        // at a time, with a new pod of the same name and no annotation.
        AtomicBoolean done = new AtomicBoolean();
        Thread operator = new Thread(() -> {
            while (!done.get()) {
                client.pods().inNamespace("kafka").list().getItems().stream()
                        .filter(p -> annotatedForRoll(p.getMetadata().getName()))
                        .findFirst()
                        .ifPresent(p -> {
                            String name = p.getMetadata().getName();
                            client.pods().inNamespace("kafka").withName(name).delete();
                            client.pods()
                                    .inNamespace("kafka")
                                    .resource(broker(
                                            name, p.getMetadata().getLabels().get("zone")))
                                    .create();
                        });
                try {
                    Thread.sleep(50);
                } catch (InterruptedException e) {
                    return;
                }
            }
        });
        operator.start();
        try {
            ChaosOutcome outcome = provider.triggerFault(rollingRestart("strimzi.io/component-type=kafka", 10))
                    .get(15, TimeUnit.SECONDS);

            assertTrue(outcome.isPass(), outcome.failureReason());
            List<String> after = uids();
            assertEquals(3, after.size());
            assertTrue(
                    after.stream().noneMatch(before::contains),
                    "the step returned only once every pod had been replaced");
        } finally {
            done.set(true);
            operator.join();
        }
    }

    @Test
    void rollingRestartThatDoesNotFinishFailsAndWithdrawsTheAnnotation() throws Exception {
        createZonedBrokers();
        provider.rollPollIntervalMs = 20;

        // Nothing rolls the pods: the Cluster Operator is down, or holds them back.
        ChaosOutcome outcome = provider.triggerFault(rollingRestart("strimzi.io/component-type=kafka,zone=alpha", 1))
                .get(5, TimeUnit.SECONDS);

        assertFalse(outcome.isPass());
        assertTrue(outcome.failureReason().contains("0 of 2 pods rolled"), outcome.failureReason());
        assertFalse(annotatedForRoll("krafter-brokers-0"), "not left for the operator to roll during a later step");
        assertFalse(annotatedForRoll("krafter-brokers-1"));
    }

    @Test
    void rollingRestartFailsWhenNoMatchingPodIsRunByAPodSetOrStatefulSet() throws Exception {
        client.pods()
                .inNamespace("kafka")
                .resource(new PodBuilder()
                        .withNewMetadata()
                        .withName("bare")
                        .withNamespace("kafka")
                        .addToLabels("app", "bare")
                        .endMetadata()
                        .build())
                .create();

        ChaosOutcome outcome =
                provider.triggerFault(rollingRestart("app=bare", 0)).get(5, TimeUnit.SECONDS);

        assertFalse(outcome.isPass());
        assertTrue(outcome.failureReason().contains("StrimziPodSet or a StatefulSet"), outcome.failureReason());
    }

    @Test
    void rollingRestartRestartsTheStatefulSetOfAKafkaNotRunByStrimzi() throws Exception {
        StatefulSet ss = new StatefulSetBuilder()
                .withNewMetadata()
                .withName("kafka")
                .withNamespace("kafka")
                .endMetadata()
                .withNewSpec()
                .withReplicas(1)
                .withNewTemplate()
                .withNewMetadata()
                .addToLabels("app", "kafka")
                .endMetadata()
                .endTemplate()
                .endSpec()
                .build();
        client.apps().statefulSets().inNamespace("kafka").resource(ss).create();
        client.pods()
                .inNamespace("kafka")
                .resource(new PodBuilder()
                        .withNewMetadata()
                        .withName("kafka-0")
                        .withNamespace("kafka")
                        .addToLabels("app", "kafka")
                        .addNewOwnerReference()
                        .withApiVersion("apps/v1")
                        .withKind("StatefulSet")
                        .withName("kafka")
                        .withUid("sts-uid")
                        .endOwnerReference()
                        .endMetadata()
                        .build())
                .create();

        ChaosOutcome outcome =
                provider.triggerFault(rollingRestart("app=kafka", 0)).get(5, TimeUnit.SECONDS);

        assertTrue(outcome.isPass(), outcome.failureReason());
        StatefulSet after = client.apps()
                .statefulSets()
                .inNamespace("kafka")
                .withName("kafka")
                .get();
        assertTrue(
                after.getSpec()
                        .getTemplate()
                        .getMetadata()
                        .getAnnotations()
                        .containsKey("kubectl.kubernetes.io/restartedAt"),
                "pod template stamped, so the StatefulSet controller rolls the pods");
        assertFalse(annotatedForRoll("kafka-0"), "the Strimzi annotation is for StrimziPodSet pods only");
    }

    private List<String> remainingPods() {
        return client.pods().inNamespace("kafka").list().getItems().stream()
                .map(p -> p.getMetadata().getName())
                .sorted()
                .toList();
    }

    @Test
    void podKillWithTargetAllKillsEveryPodOfTheZone() throws Exception {
        createZonedBrokers();

        FaultSpec spec = FaultSpec.builder("az-failure")
                .targetLabel("strimzi.io/component-type=kafka,zone=alpha")
                .targetAll(true)
                .disruptionType(DisruptionType.POD_KILL)
                .build();

        ChaosOutcome outcome = provider.triggerFault(spec).get(5, TimeUnit.SECONDS);

        assertTrue(outcome.isPass(), outcome.failureReason());
        assertEquals(List.of("krafter-brokers-2"), remainingPods(), "both alpha brokers killed, sigma untouched");
    }

    @Test
    void podKillWithoutTargetAllKillsOnePod() throws Exception {
        createZonedBrokers();

        FaultSpec spec = FaultSpec.builder("one-of-zone")
                .targetLabel("strimzi.io/component-type=kafka,zone=alpha")
                .disruptionType(DisruptionType.POD_KILL)
                .build();

        provider.triggerFault(spec).get(5, TimeUnit.SECONDS);

        List<String> remaining = remainingPods();
        assertEquals(2, remaining.size());
        assertTrue(remaining.contains("krafter-brokers-2"));
    }

    @Test
    void podKillByBrokerIdNeverKillsAKraftController() throws Exception {
        // The default Kind cluster: brokers are nodes 0–2, dedicated controllers 3–5.
        StrimziTestCluster cluster = new StrimziTestCluster(server, client)
                .pool("brokers-alpha", "broker", 0)
                .pool("brokers-gamma", "broker", 1)
                .pool("brokers-sigma", "broker", 2)
                .pool("controllers-alpha", "controller", 3)
                .pool("controllers-gamma", "controller", 4)
                .pool("controllers-sigma", "controller", 5);

        FaultSpec spec = FaultSpec.builder("kill-3")
                .targetBrokerId(3)
                .disruptionType(DisruptionType.POD_KILL)
                .build();

        ChaosOutcome outcome = provider.triggerFault(spec).get(5, TimeUnit.SECONDS);

        assertTrue(outcome.isPass(), outcome.failureReason());
        List<String> pods = cluster.pods();
        assertEquals(
                3,
                pods.stream().filter(p -> p.startsWith("krafter-controllers-")).count(),
                "controller 3 used to go");
        assertEquals(
                2, pods.stream().filter(p -> p.startsWith("krafter-brokers-")).count(), pods.toString());
    }

    @Test
    void podKillFailsWhenTheSelectorMatchesNoPod() throws Exception {
        createZonedBrokers();

        // The az-failure selector before the fix: node labels never reach pods.
        FaultSpec spec = FaultSpec.builder("az-failure-old")
                .targetLabel("strimzi.io/component-type=kafka,topology.kubernetes.io/zone=zone-a")
                .targetAll(true)
                .disruptionType(DisruptionType.POD_KILL)
                .build();

        ChaosOutcome outcome = provider.triggerFault(spec).get(5, TimeUnit.SECONDS);

        assertFalse(outcome.isPass());
        assertTrue(outcome.failureReason().contains("No pods found"), outcome.failureReason());
        assertEquals(3, remainingPods().size());
    }

    @Test
    void testCleanupNetworkPolicies() {
        // Just verify it doesn't throw when there are no policies
        assertDoesNotThrow(() -> provider.cleanup("test-engine"));
    }

    @Test
    void testIsAvailable() {
        assertTrue(provider.isAvailable());
    }
}
