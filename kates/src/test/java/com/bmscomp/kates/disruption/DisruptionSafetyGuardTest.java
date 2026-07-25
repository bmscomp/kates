package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;

import io.fabric8.kubernetes.api.model.apps.StatefulSet;
import io.fabric8.kubernetes.api.model.apps.StatefulSetBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.server.mock.EnableKubernetesMockClient;
import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.DisruptionType;
import com.bmscomp.kates.chaos.FaultSpec;
import com.bmscomp.kates.chaos.KubernetesChaosProvider;

/**
 * Pins the SCALE_DOWN rollback fix (P0-4). The original guard derived the
 * "restore" target from {@code status/spec.replicas}, which at rollback time
 * already hold the REDUCED count — so it restored to the reduced value (a
 * silent no-op) and the cluster never recovered its brokers. The fix restores
 * from the {@code kates.io/original-replicas} snapshot the provider stamps at
 * scale-down time, then clears it.
 */
@EnableKubernetesMockClient(crud = true)
class DisruptionSafetyGuardTest {

    KubernetesMockServer server;
    KubernetesClient client;

    private DisruptionSafetyGuard guard;

    @BeforeEach
    void setup() {
        guard = new DisruptionSafetyGuard();
        guard.kubeClient = client;
    }

    @Test
    void restoreUsesSnapshotAndClearsIt() {
        // A StatefulSet already scaled down to 1, carrying the original-count
        // snapshot (3) that the provider stamped when it scaled down.
        StatefulSet ss = new StatefulSetBuilder()
                .withNewMetadata()
                .withName("kafka")
                .withNamespace("default")
                .addToLabels("app", "kafka")
                .addToAnnotations(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION, "3")
                .endMetadata()
                .withNewSpec()
                .withReplicas(1)
                .endSpec()
                .build();
        client.apps().statefulSets().inNamespace("default").resource(ss).create();

        FaultSpec spec = FaultSpec.builder("restore")
                .targetNamespace("default")
                .targetLabel("app=kafka")
                .disruptionType(DisruptionType.SCALE_DOWN)
                .build();

        guard.restoreReplicaCount(spec);

        StatefulSet after = client.apps()
                .statefulSets()
                .inNamespace("default")
                .withName("kafka")
                .get();
        assertEquals(3, after.getSpec().getReplicas(), "restored to the ORIGINAL replica count, not the reduced one");
        assertNull(
                after.getMetadata().getAnnotations().get(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION),
                "snapshot cleared so a later scale-down captures a fresh baseline");
    }

    @Test
    void restoreWithoutSnapshotDoesNotInventReplicas() {
        // No snapshot annotation: the guard must not scale a StatefulSet UP to a
        // fabricated count — it can only fall back to the current value.
        StatefulSet ss = new StatefulSetBuilder()
                .withNewMetadata()
                .withName("kafka2")
                .withNamespace("default")
                .addToLabels("app", "kafka2")
                .endMetadata()
                .withNewSpec()
                .withReplicas(1)
                .endSpec()
                .build();
        client.apps().statefulSets().inNamespace("default").resource(ss).create();

        FaultSpec spec = FaultSpec.builder("restore-none")
                .targetNamespace("default")
                .targetLabel("app=kafka2")
                .disruptionType(DisruptionType.SCALE_DOWN)
                .build();

        guard.restoreReplicaCount(spec);

        StatefulSet after = client.apps()
                .statefulSets()
                .inNamespace("default")
                .withName("kafka2")
                .get();
        assertEquals(1, after.getSpec().getReplicas());
    }
}
