package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;

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

@EnableKubernetesMockClient(crud = true)
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

        FaultSpec spec = FaultSpec.builder("scale-down")
                .targetNamespace("default")
                .targetLabel("app=kafka")
                .disruptionType(DisruptionType.SCALE_DOWN)
                .build();

        // First SCALE_DOWN: 3 → 2, and the ORIGINAL count (3) is snapshotted.
        provider.triggerFault(spec).get(5, TimeUnit.SECONDS);
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
        provider.triggerFault(spec).get(5, TimeUnit.SECONDS);
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
    }

    private void createZonedBrokers() {
        String[][] brokers = {
            {"krafter-brokers-0", "alpha"}, {"krafter-brokers-1", "alpha"}, {"krafter-brokers-2", "sigma"}
        };
        for (String[] b : brokers) {
            client.pods()
                    .inNamespace("kafka")
                    .resource(new PodBuilder()
                            .withNewMetadata()
                            .withName(b[0])
                            .withNamespace("kafka")
                            .addToLabels("strimzi.io/component-type", "kafka")
                            .addToLabels("zone", b[1])
                            .endMetadata()
                            .build())
                    .create();
        }
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
