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

    /** A pod of a Strimzi node pool, which carries its roles as labels. */
    static Pod kraftPod(String name, boolean broker, boolean controller) {
        return new PodBuilder()
                .withNewMetadata()
                .withName(name)
                .withNamespace("kafka")
                .addToLabels("strimzi.io/component-type", "kafka")
                .addToLabels("strimzi.io/broker-role", String.valueOf(broker))
                .addToLabels("strimzi.io/controller-role", String.valueOf(controller))
                .endMetadata()
                .build();
    }

    /** The default Kind cluster, controllers listed first: brokers are nodes 0–2, dedicated controllers 3–5. */
    private static final List<Pod> THREE_BROKERS_THREE_CONTROLLERS = List.of(
            kraftPod("krafter-controllers-alpha-3", false, true),
            kraftPod("krafter-controllers-gamma-4", false, true),
            kraftPod("krafter-controllers-sigma-5", false, true),
            kraftPod("krafter-brokers-alpha-0", true, false),
            kraftPod("krafter-brokers-gamma-1", true, false),
            kraftPod("krafter-brokers-sigma-2", true, false));

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
    void rollingRestartTakesEveryMatchingPodUnlessOneIsNamed() {
        // A targetBrokerId, such as the 0 a JSON spec held before it got the
        // builder defaults, must not shrink a roll to one broker.
        FaultSpec roll = FaultSpec.builder("x")
                .targetBrokerId(0)
                .disruptionType(DisruptionType.ROLLING_RESTART)
                .build();
        assertEquals(PodTargets.Mode.ALL, PodTargets.mode(roll));
        assertEquals(3, PodTargets.select(roll, BROKERS).size());

        assertEquals(
                PodTargets.Mode.NAMED_POD,
                PodTargets.mode(roll.toBuilder().targetPod("krafter-brokers-2").build()));
    }

    @Test
    void targetAllOutranksABrokerId() {
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
    void brokerIdNeverPicksAKraftController() {
        assertEquals(
                List.of("krafter-brokers-gamma-1"),
                PodTargets.select(FaultSpec.builder("x").targetBrokerId(1).build(), THREE_BROKERS_THREE_CONTROLLERS));

        // Node 3 is a dedicated controller, and used to be the pick. Among
        // brokers it matches none, so the first broker is.
        assertEquals(
                List.of("krafter-brokers-alpha-0"),
                PodTargets.select(FaultSpec.builder("x").targetBrokerId(3).build(), THREE_BROKERS_THREE_CONTROLLERS));
    }

    @Test
    void aNodeWithBothRolesIsABroker() {
        Pod dual = kraftPod("krafter-dual-0", true, true);

        assertTrue(PodTargets.isBroker(dual));
        assertEquals(
                List.of("krafter-dual-0"),
                PodTargets.select(FaultSpec.builder("x").targetBrokerId(0).build(), List.of(dual)));
    }

    @Test
    void onlyTheBrokerRoleLabelMakesAPodNotABroker() {
        assertFalse(PodTargets.isBroker(THREE_BROKERS_THREE_CONTROLLERS.getFirst()));
        assertTrue(PodTargets.isBroker(THREE_BROKERS_THREE_CONTROLLERS.getLast()));
        // Kafka not run by Strimzi carries no role label: nothing says it is not a broker.
        assertTrue(PodTargets.isBroker(pod("kafka-0", "alpha")));
    }

    @Test
    void resolveByBrokerIdFailsWhenTheSelectorMatchesOnlyControllers() {
        THREE_BROKERS_THREE_CONTROLLERS.forEach(
                p -> client.pods().inNamespace("kafka").resource(p).create());

        FaultSpec spec = FaultSpec.builder("x")
                .targetLabel("strimzi.io/controller-role=true")
                .targetBrokerId(3)
                .build();

        IllegalStateException e = assertThrows(IllegalStateException.class, () -> PodTargets.resolve(client, spec));
        assertTrue(e.getMessage().startsWith("targetBrokerId picks brokers only"), e.getMessage());
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
