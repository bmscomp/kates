package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import java.util.concurrent.TimeUnit;
import jakarta.inject.Inject;
import jakarta.inject.Named;

import io.fabric8.kubernetes.api.model.PodBuilder;
import io.fabric8.kubernetes.api.model.apps.StatefulSetBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * The provider as the application runs it, fault tolerance interceptors and
 * all, against an API server that carries a change out and loses its answer,
 * or fails a request. Retried as a whole, the injection ran again on what the
 * first attempt had already changed: a second random pod killed, a
 * StatefulSet scaled down from the count the first attempt left, a partition
 * created again over its own NetworkPolicy.
 */
@QuarkusTest
@QuarkusTestResource(value = LossyApiServer.class, restrictToAnnotatedClass = true)
class KubernetesChaosProviderRetryTest {

    private static final String NAMESPACE = "kafka";
    private static final String PODS = "/api/v1/namespaces/kafka/pods";
    private static final String POLICIES = "/apis/networking.k8s.io/v1/namespaces/kafka/networkpolicies";

    @Inject
    @Named("kubernetes")
    KubernetesChaosProvider provider;

    @Inject
    KubernetesClient client;

    private final LossyDispatcher server = LossyApiServer.DISPATCHER;

    @BeforeEach
    void emptyTheServer() {
        server.reset();
    }

    private void broker(String name, String statefulSet) {
        PodBuilder pod = new PodBuilder()
                .withNewMetadata()
                .withName(name)
                .withNamespace(NAMESPACE)
                .addToLabels("strimzi.io/component-type", "kafka")
                .endMetadata();
        if (statefulSet != null) {
            pod.editMetadata()
                    .addNewOwnerReference()
                    .withApiVersion("apps/v1")
                    .withKind("StatefulSet")
                    .withName(statefulSet)
                    .withUid("sts-uid")
                    .endOwnerReference()
                    .endMetadata();
        }
        client.pods().inNamespace(NAMESPACE).resource(pod.build()).create();
    }

    private List<String> pods() {
        return client.pods().inNamespace(NAMESPACE).list().getItems().stream()
                .map(p -> p.getMetadata().getName())
                .toList();
    }

    @Test
    void aPodKillWhoseAnswerIsLostKillsOnePod() throws Exception {
        broker("krafter-brokers-0", null);
        broker("krafter-brokers-1", null);
        broker("krafter-brokers-2", null);
        server.loseAnswerOnce("DELETE", PODS + "/");

        ChaosOutcome outcome = provider.triggerFault(FaultSpec.builder("kill")
                        .disruptionType(DisruptionType.POD_KILL)
                        .build())
                .get(30, TimeUnit.SECONDS);

        assertFalse(outcome.isPass(), "the client never saw its pod go");
        assertEquals(
                1, server.writes("DELETE", PODS + "/").size(), server.writes().toString());
        assertEquals(2, pods().size(), "one broker killed, not a second one picked at random: " + pods());
    }

    @Test
    void aScaleDownWhoseAnswerIsLostRemovesOneReplica() throws Exception {
        client.apps()
                .statefulSets()
                .inNamespace(NAMESPACE)
                .resource(new StatefulSetBuilder()
                        .withNewMetadata()
                        .withName("kafka")
                        .withNamespace(NAMESPACE)
                        .endMetadata()
                        .withNewSpec()
                        .withReplicas(3)
                        .endSpec()
                        .build())
                .create();
        broker("kafka-0", "kafka");
        broker("kafka-1", "kafka");
        broker("kafka-2", "kafka");
        String scale = "/apis/apps/v1/namespaces/kafka/statefulsets/kafka/scale";
        server.loseAnswerOnce("PUT", scale);

        ChaosOutcome outcome = provider.triggerFault(FaultSpec.builder("scale-down")
                        .disruptionType(DisruptionType.SCALE_DOWN)
                        .build())
                .get(30, TimeUnit.SECONDS);

        assertFalse(outcome.isPass(), "the client never saw the scale go through");
        assertEquals(List.of("PUT " + scale), server.writes("PUT", scale));
        assertEquals(
                2,
                client.apps()
                        .statefulSets()
                        .inNamespace(NAMESPACE)
                        .withName("kafka")
                        .get()
                        .getSpec()
                        .getReplicas(),
                "3 → 2, not 3 → 2 → 1");
    }

    private ChaosOutcome partition() throws Exception {
        broker("krafter-brokers-0", null);
        return provider.triggerFault(FaultSpec.builder("split")
                        .targetPod("krafter-brokers-0")
                        .disruptionType(DisruptionType.NETWORK_PARTITION)
                        .chaosDurationSec(1)
                        .build())
                .get(60, TimeUnit.SECONDS);
    }

    @Test
    void aFailedRemovalOfAPartitionIsRetried() throws Exception {
        server.failOnce("DELETE", POLICIES);
        server.failOnce("DELETE", POLICIES);

        ChaosOutcome outcome = partition();

        assertTrue(outcome.isPass(), outcome.failureReason());
        assertEquals(1, server.writes("POST", POLICIES).size(), "partitioned once: " + server.writes());
        assertEquals(3, server.writes("DELETE", POLICIES).size(), "the removal, and two retries");
        assertEquals(
                List.of(),
                client.network().networkPolicies().inAnyNamespace().list().getItems(),
                "and the partition is gone");
    }

    /**
     * Once the removal's own retries ran out, the whole injection used to be
     * retried: the partition was created again, over the NetworkPolicy still
     * there, and failed on it three times.
     */
    @Test
    void aRemovalThatKeepsFailingFailsTheFaultWithoutPartitioningAgain() throws Exception {
        for (int i = 0; i < 4; i++) {
            server.failOnce("DELETE", POLICIES);
        }

        ChaosOutcome outcome = partition();

        assertFalse(outcome.isPass());
        assertTrue(
                outcome.failureReason()
                        .startsWith("NETWORK_PARTITION: could not remove the NetworkPolicies, so the"
                                + " pods may still be cut off"),
                outcome.failureReason());
        assertEquals(1, server.writes("POST", POLICIES).size(), "partitioned once: " + server.writes());
        assertEquals(4, server.writes("DELETE", POLICIES).size(), "the removal, and its three retries");
    }

    /**
     * One circuit breaker served every fault in the application: two faults
     * that failed were enough to have the next one refused, whatever it was,
     * with an error that said nothing about it.
     */
    @Test
    void faultsThatFailDoNotRefuseTheNextOne() throws Exception {
        broker("krafter-brokers-0", null);
        FaultSpec nowhere = FaultSpec.builder("kill-nothing")
                .targetLabel("app=nothing")
                .disruptionType(DisruptionType.POD_KILL)
                .build();
        for (int i = 0; i < 4; i++) {
            ChaosOutcome failed = provider.triggerFault(nowhere).get(30, TimeUnit.SECONDS);
            assertTrue(failed.failureReason().startsWith("No pods found"), failed.failureReason());
        }

        ChaosOutcome outcome = provider.triggerFault(FaultSpec.builder("kill")
                        .disruptionType(DisruptionType.POD_KILL)
                        .build())
                .get(30, TimeUnit.SECONDS);

        assertTrue(outcome.isPass(), outcome.failureReason());
        assertEquals(List.of(), pods());
    }
}
