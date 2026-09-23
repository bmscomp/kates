package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.server.mock.EnableKubernetesMockClient;
import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.ScaleDownSnapshots;
import com.bmscomp.kates.chaos.StrimziTestCluster;

/**
 * Startup recovery of a SCALE_DOWN whose plan died with the Kates pod. It
 * used to look only at StatefulSets, which Strimzi does not create.
 */
@EnableKubernetesMockClient(crud = true)
class DisruptionOrphanReconcilerTest {

    KubernetesMockServer server;
    KubernetesClient client;

    @Test
    void restoresANodePoolAbandonedMidPlanAndLeavesARecentScaleDownAlone() {
        StrimziTestCluster cluster = new StrimziTestCluster(server, client)
                .pool("brokers-alpha", "broker", 3)
                .pool("brokers-sigma", "broker", 4)
                .pool("brokers-gamma", "broker", 5);
        cluster.scaledDown("brokers-sigma", 0);
        // Could be a plan another Kates replica is running right now.
        cluster.scaledDown("brokers-gamma", 0, System.currentTimeMillis());

        DisruptionOrphanReconciler reconciler = new DisruptionOrphanReconciler();
        reconciler.kubeClient = client;
        reconciler.kafkaNamespace = StrimziTestCluster.NAMESPACE;
        reconciler.minAgeSec = 900;

        ScaleDownSnapshots.Restored restored = reconciler.reconcileScaleDowns();

        assertEquals(new ScaleDownSnapshots.Restored(0, 1), restored);
        assertEquals(1, cluster.replicas("brokers-sigma"));
        assertTrue(cluster.annotations("brokers-sigma").isEmpty(), "snapshot cleared");
        assertEquals(0, cluster.replicas("brokers-gamma"));
        assertEquals(1, cluster.replicas("brokers-alpha"));
    }
}
