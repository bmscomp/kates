package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;

import io.fabric8.kubernetes.api.model.apps.StatefulSet;
import io.fabric8.kubernetes.api.model.apps.StatefulSetBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.NamespacedKubernetesClient;
import io.fabric8.kubernetes.client.server.mock.EnableKubernetesMockClient;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.KubernetesChaosProvider;
import com.bmscomp.kates.chaos.VertxPerMockClient;

/**
 * Releasing the lease ends a run, plan, compound or resilience alike, and gives
 * back what its SCALE_DOWNs removed. A compound or resilience run never undid
 * one, and a plan only when a step failed its recovery check: the broker stayed
 * removed until orphan recovery at the next Kates API restart.
 */
@EnableKubernetesMockClient(crud = true, kubernetesClientBuilderCustomizer = VertxPerMockClient.class)
class DisruptionConcurrencyGuardScaleDownTest {

    KubernetesClient client;

    private DisruptionConcurrencyGuard guard;

    @BeforeEach
    void setUp() {
        guard = new DisruptionConcurrencyGuard();
        guard.kafkaNamespace = "kafka";
        guard.kafkaCluster = "krafter";
        guard.kubeClient = client.adapt(NamespacedKubernetesClient.class).inNamespace("kates");
    }

    /** A StatefulSet scaled from 3 to 2 at {@code scaledDownAt}, as the provider leaves it. */
    private void scaledDown(String name, long scaledDownAt) {
        StatefulSet ss = new StatefulSetBuilder()
                .withNewMetadata()
                .withName(name)
                .withNamespace("kafka")
                .addToAnnotations(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION, "3")
                .addToAnnotations(KubernetesChaosProvider.SCALED_DOWN_AT_ANNOTATION, String.valueOf(scaledDownAt))
                .endMetadata()
                .withNewSpec()
                .withReplicas(2)
                .endSpec()
                .build();
        client.apps().statefulSets().inNamespace("kafka").resource(ss).create();
    }

    private StatefulSet get(String name) {
        return client.apps().statefulSets().inNamespace("kafka").withName(name).get();
    }

    @Test
    void releasingTheLeaseGivesBackWhatTheRunScaledDown() {
        String target = guard.currentTarget();
        guard.tryAcquire(target, "compound#1");
        scaledDown("brokers", System.currentTimeMillis());

        guard.release(target, "compound#1");

        assertEquals(3, get("brokers").getSpec().getReplicas());
        assertNull(get("brokers")
                .getMetadata()
                .getAnnotations()
                .get(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION));
    }

    /** A snapshot older than the run is not the run's: orphan recovery owns it. */
    @Test
    void aScaleDownFromBeforeTheRunIsLeftAlone() {
        scaledDown("older", System.currentTimeMillis() - 60_000);
        String target = guard.currentTarget();
        guard.tryAcquire(target, "plan#1");

        guard.release(target, "plan#1");

        assertEquals(2, get("older").getSpec().getReplicas());
    }

    /** A token that never held the lease ends no run, and restores nothing. */
    @Test
    void aReleaseByAnotherTokenRestoresNothing() {
        String target = guard.currentTarget();
        guard.tryAcquire(target, "plan#1");
        scaledDown("brokers", System.currentTimeMillis());

        guard.release(target, "plan#2");

        assertEquals(2, get("brokers").getSpec().getReplicas());
    }
}
