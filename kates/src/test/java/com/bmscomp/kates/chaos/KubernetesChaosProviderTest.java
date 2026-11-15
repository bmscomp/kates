package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.concurrent.atomic.AtomicBoolean;

import io.fabric8.kubernetes.api.model.EphemeralContainer;
import io.fabric8.kubernetes.api.model.Pod;
import io.fabric8.kubernetes.api.model.PodBuilder;
import io.fabric8.kubernetes.api.model.apps.StatefulSet;
import io.fabric8.kubernetes.api.model.apps.StatefulSetBuilder;
import io.fabric8.kubernetes.api.model.networking.v1.NetworkPolicyBuilder;
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
                .chaosDurationSec(1)
                .build();

        ChaosOutcome outcome = provider.triggerFault(spec).get(5, TimeUnit.SECONDS);

        assertNotNull(outcome);
        assertTrue(outcome.isPass());

        RecordedRequest write = server.getLastRequest();
        assertEquals("PUT", write.getMethod());
        assertEquals(subresource, write.getPath());
        Pod sent = client.getKubernetesSerialization().unmarshal(write.getUtf8Body(), Pod.class);
        assertEquals(1, sent.getSpec().getEphemeralContainers().size());
        EphemeralContainer stress = sent.getSpec().getEphemeralContainers().get(0);
        assertTrue(stress.getName().startsWith("chaos-cpu-stress-"), stress.getName());
        assertEquals("docker.io/polinux/stress:1.0.4", stress.getImage());
        // The pod names no user: stress runs as nobody.
        assertRestricted(stress, 65534L, 65534L);
    }

    /** What Pod Security "restricted" asks of a container, and the uid and gid it runs as. */
    private static void assertRestricted(EphemeralContainer stress, long uid, long gid) {
        var sc = stress.getSecurityContext();
        assertNotNull(sc);
        assertEquals(Boolean.TRUE, sc.getRunAsNonRoot());
        assertEquals(uid, sc.getRunAsUser());
        assertEquals(gid, sc.getRunAsGroup());
        assertEquals(Boolean.FALSE, sc.getAllowPrivilegeEscalation());
        assertEquals(List.of("ALL"), sc.getCapabilities().getDrop());
        assertEquals("RuntimeDefault", sc.getSeccompProfile().getType());
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
                // As kafka-cluster's pools set it; OpenShift admits only the namespace's uid range.
                .withNewSecurityContext()
                .withRunAsUser(1001L)
                .withRunAsGroup(1001L)
                .endSecurityContext()
                .addNewContainer()
                .withName("kafka")
                .withImage("kafka")
                .endContainer()
                .endSpec()
                .build();
        client.pods().inNamespace("default").resource(pod).create();

        String subresource = "/api/v1/namespaces/default/pods/broker-1/ephemeralcontainers";
        server.expect().put().withPath(subresource).andReturn(200, pod).once();
        // kates.chaos.stress-image, as a cluster without Docker Hub sets it.
        provider.stressImage = "registry.example.com/mirror/stress:1.0.4";

        FaultSpec spec = FaultSpec.builder("test-io-stress")
                .targetNamespace("default")
                .targetLabel("app=kafka2")
                .disruptionType(DisruptionType.IO_STRESS)
                .ioWorkers(4)
                .chaosDurationSec(1)
                .build();

        ChaosOutcome outcome = provider.triggerFault(spec).get(5, TimeUnit.SECONDS);

        assertNotNull(outcome);
        assertTrue(outcome.isPass());

        RecordedRequest write = server.getLastRequest();
        assertEquals("PUT", write.getMethod());
        assertEquals(subresource, write.getPath());
        Pod sent = client.getKubernetesSerialization().unmarshal(write.getUtf8Body(), Pod.class);
        assertEquals(1, sent.getSpec().getEphemeralContainers().size());
        EphemeralContainer stress = sent.getSpec().getEphemeralContainers().get(0);
        assertTrue(stress.getName().startsWith("chaos-io-stress-"), stress.getName());
        assertEquals("registry.example.com/mirror/stress:1.0.4", stress.getImage());
        // The pod's own user and group, which its namespace already admits.
        assertRestricted(stress, 1001L, 1001L);
    }

    private Pod stressTarget(String name, String app) {
        Pod pod = new PodBuilder()
                .withNewMetadata()
                .withName(name)
                .withNamespace("default")
                .addToLabels("app", app)
                .endMetadata()
                .withNewSpec()
                .addNewContainer()
                .withName("kafka")
                .withImage("kafka")
                .endContainer()
                .endSpec()
                .build();
        client.pods().inNamespace("default").resource(pod).create();
        return pod;
    }

    private static FaultSpec cpuStress(String app, int chaosDurationSec) {
        return FaultSpec.builder("test-cpu-stress-" + app)
                .targetNamespace("default")
                .targetLabel("app=" + app)
                .disruptionType(DisruptionType.CPU_STRESS)
                .cpuCores(1)
                .chaosDurationSec(chaosDurationSec)
                .build();
    }

    @Test
    void stressIsDoneOnlyOnceItsDurationIsOver() throws Exception {
        Pod pod = stressTarget("broker-4", "kafka5");
        server.expect()
                .put()
                .withPath("/api/v1/namespaces/default/pods/broker-4/ephemeralcontainers")
                .andReturn(200, pod)
                .once();

        long started = System.nanoTime();
        ChaosOutcome outcome = provider.triggerFault(cpuStress("kafka5", 2)).get(10, TimeUnit.SECONDS);
        long tookMs = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - started);

        // Done at once, the plan ended and its lease went while the stress still ran.
        assertTrue(outcome.isPass(), outcome.failureReason());
        assertTrue(tookMs >= 2000, "the stress was reported done after " + tookMs + " ms of 2000");
    }

    @Test
    void twoStressesOnOnePodNameTheirContainersApart() throws Exception {
        Pod pod = stressTarget("broker-5", "kafka6");
        server.expect()
                .put()
                .withPath("/api/v1/namespaces/default/pods/broker-5/ephemeralcontainers")
                .andReturn(200, pod)
                .times(2);

        List<String> names = new ArrayList<>();
        for (int i = 0; i < 2; i++) {
            assertTrue(provider.triggerFault(cpuStress("kafka6", 1))
                    .get(10, TimeUnit.SECONDS)
                    .isPass());
            Pod sent = client.getKubernetesSerialization()
                    .unmarshal(server.getLastRequest().getUtf8Body(), Pod.class);
            names.add(sent.getSpec().getEphemeralContainers().get(0).getName());
        }

        // A pod refuses an ephemeral container named as one it already has, and none is ever removed.
        assertNotEquals(names.get(0), names.get(1), names.toString());
    }

    @Test
    void stressNeverRunsAsRootEvenWhenThePodDoes() throws Exception {
        Pod pod = new PodBuilder()
                .withNewMetadata()
                .withName("broker-2")
                .withNamespace("default")
                .addToLabels("app", "kafka3")
                .endMetadata()
                .withNewSpec()
                .withNewSecurityContext()
                .withRunAsUser(0L)
                .withRunAsGroup(0L)
                .endSecurityContext()
                .addNewContainer()
                .withName("kafka")
                .withImage("kafka")
                .endContainer()
                .endSpec()
                .build();
        client.pods().inNamespace("default").resource(pod).create();
        server.expect()
                .put()
                .withPath("/api/v1/namespaces/default/pods/broker-2/ephemeralcontainers")
                .andReturn(200, pod)
                .once();

        FaultSpec spec = FaultSpec.builder("test-cpu-stress-root")
                .targetNamespace("default")
                .targetLabel("app=kafka3")
                .disruptionType(DisruptionType.CPU_STRESS)
                .cpuCores(1)
                .chaosDurationSec(1)
                .build();

        assertTrue(provider.triggerFault(spec).get(5, TimeUnit.SECONDS).isPass());

        Pod sent = client.getKubernetesSerialization()
                .unmarshal(server.getLastRequest().getUtf8Body(), Pod.class);
        // runAsNonRoot refuses uid 0, so stress falls back to nobody and keeps the pod's group.
        assertRestricted(sent.getSpec().getEphemeralContainers().get(0), 65534L, 0L);
    }

    @Test
    void stressTakesTheContainersUserWhereOnlyTheContainerSetsIt() throws Exception {
        // OpenShift's restricted-v2 writes the namespace's uid on each container, not the pod.
        Pod pod = new PodBuilder()
                .withNewMetadata()
                .withName("broker-3")
                .withNamespace("default")
                .addToLabels("app", "kafka4")
                .endMetadata()
                .withNewSpec()
                .addNewContainer()
                .withName("kafka")
                .withImage("kafka")
                .withNewSecurityContext()
                .withRunAsUser(1000650000L)
                .endSecurityContext()
                .endContainer()
                .endSpec()
                .build();
        client.pods().inNamespace("default").resource(pod).create();
        server.expect()
                .put()
                .withPath("/api/v1/namespaces/default/pods/broker-3/ephemeralcontainers")
                .andReturn(200, pod)
                .once();

        FaultSpec spec = FaultSpec.builder("test-cpu-stress-container-uid")
                .targetNamespace("default")
                .targetLabel("app=kafka4")
                .disruptionType(DisruptionType.CPU_STRESS)
                .cpuCores(1)
                .chaosDurationSec(1)
                .build();

        assertTrue(provider.triggerFault(spec).get(5, TimeUnit.SECONDS).isPass());

        Pod sent = client.getKubernetesSerialization()
                .unmarshal(server.getLastRequest().getUtf8Body(), Pod.class);
        assertRestricted(sent.getSpec().getEphemeralContainers().get(0), 1000650000L, 65534L);
    }

    @Test
    void testScaleDownSnapshotsOriginalReplicas() throws ExecutionException, InterruptedException, TimeoutException {
        createKafkaStatefulSet();

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

    /** A resilience run measures RPO from the moment the fault reports. */
    @Test
    void aDelayedFaultIsReportedOnceItsDelayIsOverAndBeforeThePodGoes() throws Exception {
        createZonedBrokers();
        FaultSpec spec = FaultSpec.builder("delayed-kill")
                .targetPod("krafter-brokers-2")
                .disruptionType(DisruptionType.POD_KILL)
                .delayBeforeSec(1)
                .build();
        List<Long> reported = new CopyOnWriteArrayList<>();
        List<List<String>> podsWhenReported = new CopyOnWriteArrayList<>();

        long triggered = System.nanoTime();
        ChaosOutcome outcome = provider.triggerFault(spec, injectedAt -> {
                    reported.add(injectedAt);
                    podsWhenReported.add(remainingPods());
                })
                .get(5, TimeUnit.SECONDS);

        assertTrue(outcome.isPass(), outcome.failureReason());
        assertEquals(1, reported.size(), "reported once");
        assertTrue(
                reported.getFirst() - triggered >= TimeUnit.SECONDS.toNanos(1), "reported before the delay was over");
        assertEquals(
                List.of(List.of("krafter-brokers-0", "krafter-brokers-1", "krafter-brokers-2")),
                podsWhenReported,
                "reported after the pod went");
        assertEquals(List.of("krafter-brokers-0", "krafter-brokers-1"), remainingPods());
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
    void aDelayedFaultIsTimedFromTheEndOfItsDelay() throws Exception {
        createZonedBrokers();
        // The start used to be taken before the delay, so the outcome's start
        // and duration counted the wait as part of the fault.
        FaultSpec spec = FaultSpec.builder("delayed-kill")
                .targetPod("krafter-brokers-2")
                .disruptionType(DisruptionType.POD_KILL)
                .delayBeforeSec(1)
                .build();
        long delayNanos = TimeUnit.SECONDS.toNanos(1);

        long triggered = System.nanoTime();
        ChaosOutcome outcome = provider.triggerFault(spec).get(5, TimeUnit.SECONDS);

        assertTrue(outcome.isPass(), outcome.failureReason());
        assertEquals(List.of("krafter-brokers-0", "krafter-brokers-1"), remainingPods());
        assertTrue(outcome.chaosStartNanos() - triggered >= delayNanos, "the fault's start includes the delay");
    }

    /** Every request the API server has had since the last call, as "METHOD path". */
    private List<String> requests() throws InterruptedException {
        List<String> seen = new ArrayList<>();
        for (RecordedRequest r; (r = server.takeRequest(0, TimeUnit.MILLISECONDS)) != null; ) {
            seen.add(r.getMethod() + " " + r.getPath());
        }
        return seen;
    }

    @Test
    void aPartitionWithoutADurationIsStillRemoved() throws Exception {
        createZonedBrokers();
        // The chaos limits refuse it now. One that got past them used to cut
        // the pod off until rollback, or for good.
        FaultSpec spec = FaultSpec.builder("split")
                .targetPod("krafter-brokers-0")
                .disruptionType(DisruptionType.NETWORK_PARTITION)
                .chaosDurationSec(0)
                .build();

        ChaosOutcome outcome = provider.triggerFault(spec).get(5, TimeUnit.SECONDS);

        assertTrue(outcome.isPass(), outcome.failureReason());
        assertTrue(
                requests().contains("POST /apis/networking.k8s.io/v1/namespaces/kafka/networkpolicies"),
                "the partition went in");
        assertEquals(
                List.of(),
                client.network().networkPolicies().inAnyNamespace().list().getItems(),
                "and came out");
    }

    /**
     * A partition that failed after isolating some pods left them cut off: a
     * failed fault gets no rollback, and nothing else removed the policies.
     */
    @Test
    void aPartitionThatFailsHalfWayIsRemoved() throws Exception {
        createZonedBrokers();
        // Left by an earlier fault, so isolating krafter-brokers-1 fails with a conflict.
        client.network()
                .networkPolicies()
                .inNamespace("kafka")
                .resource(new NetworkPolicyBuilder()
                        .withNewMetadata()
                        .withName("kates-netpol-krafter-brokers-1")
                        .addToLabels("managed-by", "kates")
                        .endMetadata()
                        .build())
                .create();
        FaultSpec spec = FaultSpec.builder("split-zone")
                .targetLabel("strimzi.io/component-type=kafka,zone=alpha")
                .targetAll(true)
                .disruptionType(DisruptionType.NETWORK_PARTITION)
                .chaosDurationSec(60)
                .build();

        ChaosOutcome outcome = provider.triggerFault(spec).get(5, TimeUnit.SECONDS);

        assertFalse(outcome.isPass());
        assertEquals(
                List.of(),
                client.network().networkPolicies().inAnyNamespace().list().getItems());
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

    /** A Kafka not run by Strimzi: three pods of the StatefulSet {@code kafka} in {@code default}. */
    private void createKafkaStatefulSet() {
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
    }

    private static com.bmscomp.kates.quorum.Quorum quorumAt(long term, boolean leads) {
        com.bmscomp.kates.quorum.Quorum quorum = org.mockito.Mockito.mock(com.bmscomp.kates.quorum.Quorum.class);
        org.mockito.Mockito.when(quorum.term()).thenReturn(term);
        if (leads) {
            org.mockito.Mockito.when(quorum.requireWrites()).thenReturn(term);
        } else {
            org.mockito.Mockito.doThrow(new com.bmscomp.kates.engine.NotLeaderException("m1"))
                    .when(quorum)
                    .requireWrites();
        }
        return quorum;
    }

    private static void assertMarked(java.util.Map<String, String> annotations, String term) {
        assertEquals(term, annotations.get(FaultMarks.TERM), "the term of the leader that wrote it");
        java.time.Instant deadline = java.time.Instant.parse(annotations.get(FaultMarks.DEADLINE));
        java.time.Instant expected = java.time.Instant.now().plusMillis(7_200_000);
        assertTrue(
                Math.abs(java.time.Duration.between(expected, deadline).toSeconds()) < 5,
                "the engine's run bound from now: " + deadline);
    }

    @Test
    void aMemberThatDoesNotLeadInjectsNothing() throws Exception {
        createZonedBrokers();
        createKafkaStatefulSet();
        provider.quorum = quorumAt(3, false);
        requests();
        for (DisruptionType type : List.of(
                DisruptionType.POD_KILL,
                DisruptionType.NETWORK_PARTITION,
                DisruptionType.SCALE_DOWN,
                DisruptionType.ROLLING_RESTART,
                DisruptionType.CPU_STRESS)) {
            FaultSpec spec = type == DisruptionType.SCALE_DOWN
                    ? FaultSpec.builder("x")
                            .targetNamespace("default")
                            .targetLabel("app=kafka")
                            .disruptionType(type)
                            .build()
                    : FaultSpec.builder("x")
                            .targetPod("krafter-brokers-0")
                            .disruptionType(type)
                            .chaosDurationSec(1)
                            .build();

            ChaosOutcome outcome = provider.triggerFault(spec).get(5, TimeUnit.SECONDS);

            assertFalse(outcome.isPass(), type + " went in");
            assertTrue(outcome.failureReason().contains("does not lead"), type + ": " + outcome.failureReason());
        }
        List<String> writes =
                requests().stream().filter(r -> !r.startsWith("GET ")).toList();
        assertEquals(List.of(), writes, "a member that does not lead writes nothing");
    }

    @Test
    void aPartitionCarriesItsTermAndDeadline() throws Exception {
        createZonedBrokers();
        provider.quorum = quorumAt(7, true);
        FaultSpec spec = FaultSpec.builder("split")
                .targetPod("krafter-brokers-0")
                .disruptionType(DisruptionType.NETWORK_PARTITION)
                .chaosDurationSec(30)
                .build();

        provider.triggerFault(spec);
        var policy = await(() -> client.network()
                .networkPolicies()
                .inNamespace("kafka")
                .withName("kates-netpol-krafter-brokers-0")
                .get());

        assertMarked(policy.getMetadata().getAnnotations(), "7");
    }

    @Test
    void aScaleDownCarriesItsTermAndDeadline() throws Exception {
        createKafkaStatefulSet();
        provider.quorum = quorumAt(7, true);
        FaultSpec spec = FaultSpec.builder("scale-down")
                .targetNamespace("default")
                .targetLabel("app=kafka")
                .disruptionType(DisruptionType.SCALE_DOWN)
                .build();

        assertTrue(provider.triggerFault(spec).get(5, TimeUnit.SECONDS).isPass());

        StatefulSet after = client.apps()
                .statefulSets()
                .inNamespace("default")
                .withName("kafka")
                .get();
        assertMarked(after.getMetadata().getAnnotations(), "7");
    }

    private static <T> T await(java.util.function.Supplier<T> found) throws InterruptedException {
        for (int i = 0; i < 50; i++) {
            T value = found.get();
            if (value != null) {
                return value;
            }
            TimeUnit.MILLISECONDS.sleep(100);
        }
        throw new AssertionError("not within 5 s");
    }

    @Test
    void aFaultIsMarkedWithTheTermItWasConfirmedIn() throws Exception {
        createZonedBrokers();
        // Confirmed in term 7; by the time the object is built the member has heard of term 8.
        provider.quorum = quorumAt(7, true);
        org.mockito.Mockito.when(provider.quorum.term()).thenReturn(8L);
        FaultSpec spec = FaultSpec.builder("split")
                .targetPod("krafter-brokers-0")
                .disruptionType(DisruptionType.NETWORK_PARTITION)
                .chaosDurationSec(30)
                .build();

        provider.triggerFault(spec);
        var policy = await(() -> client.network()
                .networkPolicies()
                .inNamespace("kafka")
                .withName("kates-netpol-krafter-brokers-0")
                .get());

        assertMarked(policy.getMetadata().getAnnotations(), "7");
    }

    // ---- MEMORY_STRESS, DISK_FILL and NODE_DRAIN on the Kubernetes API ----

    @Test
    void memoryStressHoldsMemoryMbInAStressContainer() throws Exception {
        Pod pod = stressTarget("broker-6", "kafka7");
        String subresource = "/api/v1/namespaces/default/pods/broker-6/ephemeralcontainers";
        server.expect().put().withPath(subresource).andReturn(200, pod).once();
        FaultSpec spec = FaultSpec.builder("test-memory-stress")
                .targetNamespace("default")
                .targetLabel("app=kafka7")
                .disruptionType(DisruptionType.MEMORY_STRESS)
                .memoryMb(256)
                .chaosDurationSec(1)
                .build();

        assertTrue(provider.triggerFault(spec).get(5, TimeUnit.SECONDS).isPass());

        Pod sent = client.getKubernetesSerialization()
                .unmarshal(server.getLastRequest().getUtf8Body(), Pod.class);
        EphemeralContainer stress = sent.getSpec().getEphemeralContainers().get(0);
        assertTrue(stress.getName().startsWith("chaos-memory-stress-"), stress.getName());
        assertEquals(
                List.of("stress", "--vm", "1", "--vm-bytes", "256M", "--vm-keep", "--timeout", "1s"),
                stress.getCommand());
        assertRestricted(stress, 65534L, 65534L);
    }

    private Pod broker(String name, String app, io.fabric8.kubernetes.api.model.Volume... volumes) {
        var container = new io.fabric8.kubernetes.api.model.ContainerBuilder()
                .withName("kafka")
                .withImage("kafka");
        for (var v : volumes) {
            container
                    .addNewVolumeMount()
                    .withName(v.getName())
                    .withMountPath("/var/lib/kafka/" + v.getName())
                    .endVolumeMount();
        }
        Pod pod = new PodBuilder()
                .withNewMetadata()
                .withName(name)
                .withNamespace("default")
                .addToLabels("app", app)
                .endMetadata()
                .withNewSpec()
                .withNewSecurityContext()
                .withRunAsUser(1001L)
                .withRunAsGroup(1001L)
                .endSecurityContext()
                .withContainers(container.build())
                .withVolumes(volumes)
                .endSpec()
                .build();
        client.pods().inNamespace("default").resource(pod).create();
        return pod;
    }

    private static io.fabric8.kubernetes.api.model.Volume pvc(String name) {
        return new io.fabric8.kubernetes.api.model.VolumeBuilder()
                .withName(name)
                .withNewPersistentVolumeClaim()
                .withClaimName(name + "-claim")
                .endPersistentVolumeClaim()
                .build();
    }

    private static io.fabric8.kubernetes.api.model.Volume emptyDir(String name) {
        return new io.fabric8.kubernetes.api.model.VolumeBuilder()
                .withName(name)
                .withNewEmptyDir()
                .endEmptyDir()
                .build();
    }

    @Test
    void diskFillMountsTheBrokersDataVolumeAndFillsItAsTheBroker() throws Exception {
        Pod pod = broker("broker-7", "kafka8", emptyDir("strimzi-tmp"), pvc("data-0"));
        String subresource = "/api/v1/namespaces/default/pods/broker-7/ephemeralcontainers";
        server.expect().put().withPath(subresource).andReturn(200, pod).once();
        FaultSpec spec = FaultSpec.builder("test-disk-fill")
                .targetNamespace("default")
                .targetLabel("app=kafka8")
                .disruptionType(DisruptionType.DISK_FILL)
                .fillPercentage(90)
                .chaosDurationSec(1)
                .build();

        ChaosOutcome outcome = provider.triggerFault(spec).get(5, TimeUnit.SECONDS);

        assertTrue(outcome.isPass(), outcome.failureReason());
        RecordedRequest write = server.takeRequest(0, TimeUnit.SECONDS);
        while (write != null && !subresource.equals(write.getPath())) {
            write = server.takeRequest(0, TimeUnit.SECONDS);
        }
        assertNotNull(write, "the fill container was added");
        Pod sent = client.getKubernetesSerialization().unmarshal(write.getUtf8Body(), Pod.class);
        EphemeralContainer fill = sent.getSpec().getEphemeralContainers().get(0);
        assertTrue(fill.getName().startsWith("chaos-disk-fill-"), fill.getName());
        assertEquals(1, fill.getVolumeMounts().size());
        assertEquals("data-0", fill.getVolumeMounts().get(0).getName());
        assertEquals(
                KubernetesChaosProvider.FILL_MOUNT,
                fill.getVolumeMounts().get(0).getMountPath());
        assertEquals(List.of("sh", "-c"), fill.getCommand().subList(0, 2));
        assertTrue(
                fill.getCommand().get(2).contains("*90/100"), fill.getCommand().get(2));
        assertRestricted(fill, 1001L, 1001L);
    }

    @Test
    void diskFillFailsWhenTheFillContainerExitedWithAnError() throws Exception {
        Pod pod = broker("broker-8", "kafka9", pvc("data-0"));
        server.expect()
                .put()
                .withPath("/api/v1/namespaces/default/pods/broker-8/ephemeralcontainers")
                .andReturn(200, pod)
                .once();
        FaultSpec spec = FaultSpec.builder("test-disk-fill-fails")
                .targetNamespace("default")
                .targetPod("broker-8")
                .disruptionType(DisruptionType.DISK_FILL)
                .chaosDurationSec(2)
                .build();

        var future = provider.triggerFault(spec);
        // While the fault holds the volume, the container exits 1, as when it may write nowhere.
        RecordedRequest write = null;
        for (int i = 0; i < 50 && write == null; i++) {
            RecordedRequest r = server.takeRequest(100, TimeUnit.MILLISECONDS);
            if (r != null && r.getPath().endsWith("/ephemeralcontainers")) {
                write = r;
            }
        }
        assertNotNull(write);
        String container = client.getKubernetesSerialization()
                .unmarshal(write.getUtf8Body(), Pod.class)
                .getSpec()
                .getEphemeralContainers()
                .get(0)
                .getName();
        client.pods()
                .inNamespace("default")
                .withName("broker-8")
                .editStatus(p -> new PodBuilder(p)
                        .withNewStatus()
                        .addNewEphemeralContainerStatus()
                        .withName(container)
                        .withNewState()
                        .withNewTerminated()
                        .withExitCode(1)
                        .endTerminated()
                        .endState()
                        .endEphemeralContainerStatus()
                        .endStatus()
                        .build());

        ChaosOutcome outcome = future.get(10, TimeUnit.SECONDS);

        assertFalse(outcome.isPass());
        assertTrue(outcome.failureReason().contains("exited 1"), outcome.failureReason());
    }

    @Test
    void diskFillPicksAClaimThenAVolumeNamedDataAndRefusesAPodWithNeither() {
        assertEquals("data-1", KubernetesChaosProvider.dataVolume(podWith(emptyDir("data-0"), pvc("data-1"))));
        assertEquals(
                "data-0", KubernetesChaosProvider.dataVolume(podWith(emptyDir("strimzi-tmp"), emptyDir("data-0"))));
        IllegalStateException e = assertThrows(
                IllegalStateException.class, () -> KubernetesChaosProvider.dataVolume(podWith(emptyDir("tmp"))));
        assertTrue(e.getMessage().contains("no data volume"), e.getMessage());
    }

    private static Pod podWith(io.fabric8.kubernetes.api.model.Volume... volumes) {
        var container = new io.fabric8.kubernetes.api.model.ContainerBuilder().withName("kafka");
        for (var v : volumes) {
            container
                    .addNewVolumeMount()
                    .withName(v.getName())
                    .withMountPath("/" + v.getName())
                    .endVolumeMount();
        }
        return new PodBuilder()
                .withNewMetadata()
                .withName("broker")
                .endMetadata()
                .withNewSpec()
                .withContainers(container.build())
                .withVolumes(volumes)
                .endSpec()
                .build();
    }

    private void node(String name, boolean unschedulable) {
        client.nodes()
                .resource(new io.fabric8.kubernetes.api.model.NodeBuilder()
                        .withNewMetadata()
                        .withName(name)
                        .endMetadata()
                        .withNewSpec()
                        .withUnschedulable(unschedulable ? true : null)
                        .endSpec()
                        .build())
                .create();
    }

    private void podOn(String namespace, String name, String node, String app, String ownerKind) {
        var meta = new io.fabric8.kubernetes.api.model.ObjectMetaBuilder()
                .withName(name)
                .withNamespace(namespace)
                .addToLabels("app", app);
        if (ownerKind != null) {
            meta.addNewOwnerReference()
                    .withKind(ownerKind)
                    .withName(name + "-owner")
                    .withApiVersion("apps/v1")
                    .withUid(name + "-uid")
                    .endOwnerReference();
        }
        client.pods()
                .inNamespace(namespace)
                .resource(new PodBuilder()
                        .withMetadata(meta.build())
                        .withNewSpec()
                        .withNodeName(node)
                        .addNewContainer()
                        .withName("c")
                        .withImage("c")
                        .endContainer()
                        .endSpec()
                        .build())
                .create();
    }

    /** The CRUD mock filters by no spec.nodeName field selector, which the API server does: answer it here. */
    private void podsOnNodeListed(String node) {
        var pods = client.pods().inAnyNamespace().list().getItems().stream()
                .filter(p -> node.equals(p.getSpec().getNodeName()))
                .toList();
        server.expect()
                .get()
                .withPath("/api/v1/pods?fieldSelector=spec.nodeName%3D" + node)
                .andReturn(
                        200,
                        new io.fabric8.kubernetes.api.model.PodListBuilder()
                                .withItems(pods)
                                .build())
                .always();
    }

    private static String eviction(String namespace, String pod) {
        return "/api/v1/namespaces/" + namespace + "/pods/" + pod + "/eviction";
    }

    private List<String> requestPaths() throws InterruptedException {
        List<String> paths = new ArrayList<>();
        RecordedRequest r;
        while ((r = server.takeRequest(0, TimeUnit.SECONDS)) != null) {
            paths.add(r.getMethod() + " " + r.getPath());
        }
        return paths;
    }

    @Test
    void nodeDrainCordonsEvictsEveryPodButDaemonSetsAndUncordons() throws Exception {
        node("node-a", false);
        node("node-b", false);
        podOn("kafka", "broker-0", "node-a", "drain1", "StrimziPodSet");
        podOn("other", "web-0", "node-a", "web", "ReplicaSet");
        podOn("kube-system", "proxy-a", "node-a", "proxy", "DaemonSet");
        podOn("kafka", "broker-1", "node-b", "drain1", "StrimziPodSet");
        podsOnNodeListed("node-a");
        server.expect()
                .post()
                .withPath(eviction("kafka", "broker-0"))
                .andReturn(201, "{}")
                .once();
        server.expect()
                .post()
                .withPath(eviction("other", "web-0"))
                .andReturn(201, "{}")
                .once();
        FaultSpec spec = FaultSpec.builder("drain")
                .targetNamespace("kafka")
                .targetPod("broker-0")
                .disruptionType(DisruptionType.NODE_DRAIN)
                .chaosDurationSec(1)
                .build();

        ChaosOutcome outcome = provider.triggerFault(spec).get(10, TimeUnit.SECONDS);

        assertTrue(outcome.isPass(), outcome.failureReason());
        List<String> paths = requestPaths();
        assertTrue(paths.contains("POST " + eviction("kafka", "broker-0")), paths.toString());
        assertTrue(paths.contains("POST " + eviction("other", "web-0")), paths.toString());
        assertFalse(paths.stream().anyMatch(p -> p.contains("proxy-a/eviction")), "a DaemonSet's pod stays");
        assertFalse(paths.stream().anyMatch(p -> p.contains("broker-1/eviction")), "another node's pod stays");
        int cordon = paths.indexOf("PATCH /api/v1/nodes/node-a");
        assertTrue(cordon >= 0 && cordon < paths.indexOf("POST " + eviction("kafka", "broker-0")), paths.toString());
        var after = client.nodes().withName("node-a").get();
        assertNull(after.getSpec().getUnschedulable(), "uncordoned");
        assertFalse(after.getMetadata().getLabels() != null
                && after.getMetadata().getLabels().containsKey(KubernetesChaosProvider.CORDONED_LABEL));
    }

    @Test
    void nodeDrainTriesABudgetsRefusalAgainAndFailsWhenTheDurationEnds() throws Exception {
        node("node-c", false);
        podOn("kafka", "broker-2", "node-c", "drain2", "StrimziPodSet");
        podsOnNodeListed("node-c");
        server.expect()
                .post()
                .withPath(eviction("kafka", "broker-2"))
                .andReturn(429, "{\"kind\":\"Status\",\"code\":429}")
                .always();
        provider.drainPollIntervalMs = 100;
        FaultSpec spec = FaultSpec.builder("drain")
                .targetNamespace("kafka")
                .targetPod("broker-2")
                .disruptionType(DisruptionType.NODE_DRAIN)
                .chaosDurationSec(1)
                .build();

        ChaosOutcome outcome;
        // The client retries a 429 itself, request-retry-backoff-limit times (2 in the application,
        // 10 in the mock's): with none, each refusal comes back at once and the provider tries again.
        try (KubernetesClient noRetry = new io.fabric8.kubernetes.client.KubernetesClientBuilder()
                .withConfig(new io.fabric8.kubernetes.client.ConfigBuilder(client.getConfiguration())
                        .withRequestRetryBackoffLimit(0)
                        .build())
                .build()) {
            provider.client = noRetry;
            outcome = provider.triggerFault(spec).get(10, TimeUnit.SECONDS);
        }

        assertFalse(outcome.isPass());
        assertTrue(outcome.failureReason().contains("kafka/broker-2"), outcome.failureReason());
        assertTrue(outcome.failureReason().contains("PodDisruptionBudget"), outcome.failureReason());
        long tries = requestPaths().stream()
                .filter(p -> p.equals("POST " + eviction("kafka", "broker-2")))
                .count();
        assertTrue(tries > 2, "tried " + tries + " times");
        assertNull(client.nodes().withName("node-c").get().getSpec().getUnschedulable(), "uncordoned all the same");
    }

    @Test
    void nodeDrainLeavesANodeSomeoneElseCordonedAlone() throws Exception {
        node("node-d", true);
        podOn("kafka", "broker-3", "node-d", "drain3", "StrimziPodSet");
        FaultSpec spec = FaultSpec.builder("drain")
                .targetNamespace("kafka")
                .targetPod("broker-3")
                .disruptionType(DisruptionType.NODE_DRAIN)
                .chaosDurationSec(1)
                .build();

        ChaosOutcome outcome = provider.triggerFault(spec).get(10, TimeUnit.SECONDS);

        assertFalse(outcome.isPass());
        assertTrue(outcome.failureReason().contains("already cordoned"), outcome.failureReason());
        assertEquals(
                Boolean.TRUE, client.nodes().withName("node-d").get().getSpec().getUnschedulable());
        assertFalse(requestPaths().stream().anyMatch(p -> p.startsWith("PATCH ")), "nothing was written");
    }

    @Test
    void nodeDrainNeverDrainsTheKatesApisNode() throws Exception {
        node("node-e", false);
        provider.katesNode = new KatesNode() {
            @Override
            public java.util.Optional<String> name() {
                return java.util.Optional.of("node-e");
            }
        };
        FaultSpec spec = FaultSpec.builder("drain")
                .targetNamespace("kafka")
                .disruptionType(DisruptionType.NODE_DRAIN)
                .envOverrides(java.util.Map.of("TARGET_NODE", "node-e"))
                .chaosDurationSec(1)
                .build();

        ChaosOutcome outcome = provider.triggerFault(spec).get(10, TimeUnit.SECONDS);

        assertFalse(outcome.isPass());
        assertTrue(outcome.failureReason().contains("the node the Kates API runs on"), outcome.failureReason());
        assertNull(client.nodes().withName("node-e").get().getSpec().getUnschedulable());
    }

    @Test
    void nodeDrainByNodeLabelTakesAMatchingNodeOtherThanTheKatesApis() throws Exception {
        for (String name : List.of("zone-a-1", "zone-a-2", "zone-b-1")) {
            client.nodes()
                    .resource(new io.fabric8.kubernetes.api.model.NodeBuilder()
                            .withNewMetadata()
                            .withName(name)
                            .addToLabels("zone", name.substring(0, 6))
                            .endMetadata()
                            .build())
                    .create();
        }
        provider.katesNode = new KatesNode() {
            @Override
            public java.util.Optional<String> name() {
                return java.util.Optional.of("zone-a-1");
            }
        };
        FaultSpec spec = FaultSpec.builder("drain")
                .targetNamespace("kafka")
                .disruptionType(DisruptionType.NODE_DRAIN)
                .envOverrides(java.util.Map.of("NODE_LABEL", "zone=zone-a"))
                .chaosDurationSec(1)
                .build();

        for (int i = 0; i < 5; i++) {
            assertEquals("zone-a-2", DrainTargets.node(client, spec, provider.katesNode.name()));
        }
        assertTrue(provider.triggerFault(spec).get(10, TimeUnit.SECONDS).isPass());
        assertTrue(requestPaths().contains("PATCH /api/v1/nodes/zone-a-2"));
    }

    @Test
    void isEvictableSkipsWhatKubectlDrainSkips() {
        Pod plain =
                new PodBuilder().withNewMetadata().withName("p").endMetadata().build();
        assertTrue(KubernetesChaosProvider.isEvictable(plain));
        assertFalse(KubernetesChaosProvider.isEvictable(new PodBuilder(plain)
                .editMetadata()
                .addToAnnotations("kubernetes.io/config.mirror", "x")
                .endMetadata()
                .build()));
        assertFalse(KubernetesChaosProvider.isEvictable(new PodBuilder(plain)
                .editMetadata()
                .withDeletionTimestamp("2026-11-15T00:00:00Z")
                .endMetadata()
                .build()));
        assertFalse(KubernetesChaosProvider.isEvictable(new PodBuilder(plain)
                .withNewStatus()
                .withPhase("Succeeded")
                .endStatus()
                .build()));
    }
}
