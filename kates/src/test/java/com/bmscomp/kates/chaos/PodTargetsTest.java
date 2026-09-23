package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;

import io.fabric8.kubernetes.api.model.Pod;
import io.fabric8.kubernetes.api.model.PodBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.server.mock.EnableKubernetesMockClient;
import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;
import org.junit.jupiter.api.Test;

@EnableKubernetesMockClient(crud = true)
class PodTargetsTest {

    KubernetesMockServer server;
    KubernetesClient client;

    static Pod pod(String name, String zone) {
        return new PodBuilder()
                .withNewMetadata()
                .withName(name)
                .withNamespace("kafka")
                .addToLabels("strimzi.io/component-type", "kafka")
                .addToLabels("zone", zone)
                .endMetadata()
                .build();
    }

    private static final List<Pod> BROKERS = List.of(
            pod("krafter-brokers-1", "alpha"), pod("krafter-brokers-11", "alpha"), pod("krafter-brokers-2", "sigma"));

    @Test
    void precedenceIsNamedPodThenAllThenBrokerIdThenRandom() {
        assertEquals(
                PodTargets.Mode.NAMED_POD,
                PodTargets.mode(FaultSpec.builder("x")
                        .targetPod("p")
                        .targetAll(true)
                        .targetBrokerId(1)
                        .build()));
        assertEquals(
                PodTargets.Mode.ALL,
                PodTargets.mode(
                        FaultSpec.builder("x").targetAll(true).targetBrokerId(1).build()));
        assertEquals(
                PodTargets.Mode.BROKER_ID,
                PodTargets.mode(FaultSpec.builder("x").targetBrokerId(1).build()));
        assertEquals(
                PodTargets.Mode.ONE_RANDOM,
                PodTargets.mode(FaultSpec.builder("x").build()));
    }

    @Test
    void targetAllOutranksTheBrokerIdAJsonSpecDefaultsTo() {
        // A FaultSpec deserialized from JSON without targetBrokerId holds 0.
        FaultSpec spec =
                FaultSpec.builder("x").targetAll(true).targetBrokerId(0).build();
        assertEquals(
                List.of("krafter-brokers-1", "krafter-brokers-11", "krafter-brokers-2"),
                PodTargets.select(spec, BROKERS));
    }

    @Test
    void brokerIdMatchesTheWholeOrdinal() {
        FaultSpec spec = FaultSpec.builder("x").targetBrokerId(1).build();
        assertEquals(List.of("krafter-brokers-1"), PodTargets.select(spec, List.of(BROKERS.get(1), BROKERS.get(0))));
    }

    @Test
    void unknownBrokerIdFallsBackToTheFirstMatch() {
        FaultSpec spec = FaultSpec.builder("x").targetBrokerId(7).build();
        assertEquals(List.of("krafter-brokers-1"), PodTargets.select(spec, BROKERS));
    }

    @Test
    void randomPicksExactlyOneMatch() {
        List<String> picked = PodTargets.select(FaultSpec.builder("x").build(), BROKERS);
        assertEquals(1, picked.size());
        assertTrue(BROKERS.stream().anyMatch(p -> p.getMetadata().getName().equals(picked.getFirst())));
    }

    @Test
    void nothingMatchingSelectsNothing() {
        assertTrue(PodTargets.select(FaultSpec.builder("x").targetAll(true).build(), List.of())
                .isEmpty());
        assertTrue(PodTargets.select(FaultSpec.builder("x").targetBrokerId(1).build(), List.of())
                .isEmpty());
        assertTrue(PodTargets.select(FaultSpec.builder("x").build(), List.of()).isEmpty());
    }

    @Test
    void resolveTargetsEveryPodOfTheZone() {
        BROKERS.forEach(p -> client.pods().inNamespace("kafka").resource(p).create());

        FaultSpec spec = FaultSpec.builder("az")
                .targetLabel("strimzi.io/component-type=kafka,zone=alpha")
                .targetAll(true)
                .build();

        assertEquals(
                List.of("krafter-brokers-1", "krafter-brokers-11"),
                PodTargets.resolve(client, spec).stream().sorted().toList());
    }

    @Test
    void resolveFailsLoudlyWhenTheSelectorMatchesNothing() {
        BROKERS.forEach(p -> client.pods().inNamespace("kafka").resource(p).create());

        FaultSpec spec = FaultSpec.builder("az")
                .targetLabel("strimzi.io/component-type=kafka,topology.kubernetes.io/zone=zone-a")
                .targetAll(true)
                .build();

        IllegalStateException e = assertThrows(IllegalStateException.class, () -> PodTargets.resolve(client, spec));
        assertTrue(e.getMessage().contains("topology.kubernetes.io/zone=zone-a"), e.getMessage());
    }

    @Test
    void namedPodSkipsTheLookup() {
        FaultSpec spec = FaultSpec.builder("x")
                .targetPod("krafter-brokers-9")
                .targetLabel("")
                .build();
        assertEquals(List.of("krafter-brokers-9"), PodTargets.resolve(client, spec));
    }
}
