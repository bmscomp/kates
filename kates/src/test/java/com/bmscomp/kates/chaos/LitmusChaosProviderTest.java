package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import java.util.Arrays;
import java.util.List;

import io.fabric8.kubernetes.api.model.PodBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.server.mock.EnableKubernetesMockClient;
import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.litmus.ChaosEngine;

/**
 * TARGET_PODS used to be one random pod whatever the spec asked for:
 * targetBrokerId was ignored and a zone-wide fault hit a single broker.
 */
@EnableKubernetesMockClient(crud = true)
class LitmusChaosProviderTest {

    KubernetesMockServer server;
    KubernetesClient client;

    private LitmusChaosProvider provider;

    @BeforeEach
    void setup() {
        provider = new LitmusChaosProvider();
        provider.client = client;
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

    private static String env(ChaosEngine engine, String name) {
        return engine.getSpec().experiments.getFirst().spec.components.env.stream()
                .filter(e -> e.name.equals(name))
                .map(e -> e.value)
                .findFirst()
                .orElse(null);
    }

    private ChaosEngine build(FaultSpec spec) {
        return provider.buildChaosEngine(spec, "engine", "pod-delete");
    }

    @Test
    void targetAllListsEveryPodOfTheZone() {
        FaultSpec spec = FaultSpec.builder("az-failure")
                .targetLabel("strimzi.io/component-type=kafka,zone=alpha")
                .targetAll(true)
                .disruptionType(DisruptionType.POD_KILL)
                .build();

        String targets = env(build(spec), "TARGET_PODS");

        assertEquals(
                List.of("krafter-brokers-0", "krafter-brokers-1"),
                Arrays.stream(targets.split(",")).sorted().toList());
    }

    @Test
    void targetBrokerIdIsHonoured() {
        FaultSpec spec = FaultSpec.builder("kill-2")
                .targetBrokerId(2)
                .disruptionType(DisruptionType.POD_KILL)
                .build();

        assertEquals("krafter-brokers-2", env(build(spec), "TARGET_PODS"));
    }

    @Test
    void labelTargetWithoutTargetAllIsOnePod() {
        FaultSpec spec = FaultSpec.builder("one")
                .targetLabel("zone=alpha")
                .disruptionType(DisruptionType.POD_KILL)
                .build();

        String targets = env(build(spec), "TARGET_PODS");

        assertTrue(List.of("krafter-brokers-0", "krafter-brokers-1").contains(targets), targets);
    }

    @Test
    void selectorMatchingNoPodIsAnError() {
        // Used to create a ChaosEngine with no TARGET_PODS at all.
        FaultSpec spec = FaultSpec.builder("az-failure-old")
                .targetLabel("strimzi.io/component-type=kafka,topology.kubernetes.io/zone=zone-a")
                .targetAll(true)
                .disruptionType(DisruptionType.POD_KILL)
                .build();

        assertThrows(IllegalStateException.class, () -> build(spec));
    }

    @Test
    void nodeDrainTakesNoTargetPods() {
        FaultSpec spec = FaultSpec.builder("drain")
                .disruptionType(DisruptionType.NODE_DRAIN)
                .build();

        assertNull(env(provider.buildChaosEngine(spec, "engine", "node-drain"), "TARGET_PODS"));
    }
}
