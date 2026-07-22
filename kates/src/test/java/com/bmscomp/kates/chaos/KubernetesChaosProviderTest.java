package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;

import io.fabric8.kubernetes.api.model.Pod;
import io.fabric8.kubernetes.api.model.PodBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.server.mock.EnableKubernetesMockClient;
import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;
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

        Pod updatedPod =
                client.pods().inNamespace("default").withName("broker-0").get();
        assertNotNull(updatedPod);
        assertEquals(1, updatedPod.getSpec().getEphemeralContainers().size());
        assertEquals(
                "chaos-cpu-stress",
                updatedPod.getSpec().getEphemeralContainers().get(0).getName());
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

        Pod updatedPod =
                client.pods().inNamespace("default").withName("broker-1").get();
        assertNotNull(updatedPod);
        assertEquals(1, updatedPod.getSpec().getEphemeralContainers().size());
        assertEquals(
                "chaos-io-stress",
                updatedPod.getSpec().getEphemeralContainers().get(0).getName());
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
