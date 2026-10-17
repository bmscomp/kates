package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.time.Instant;
import java.util.Map;

import io.fabric8.kubernetes.api.model.networking.v1.NetworkPolicy;
import io.fabric8.kubernetes.api.model.networking.v1.NetworkPolicyBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.server.mock.EnableKubernetesMockClient;
import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.ScaleDownSnapshots;
import com.bmscomp.kates.chaos.StrimziTestCluster;
import com.bmscomp.kates.chaos.VertxPerMockClient;

/**
 * Startup recovery of the faults a plan left when it died with the Kates pod.
 * The scale-down recovery used to look only at StatefulSets, which Strimzi does
 * not create, and every recovery used to skip a fault less than 15 minutes old,
 * which is every fault a normal restart leaves.
 */
@EnableKubernetesMockClient(crud = true, kubernetesClientBuilderCustomizer = VertxPerMockClient.class)
class DisruptionOrphanReconcilerTest {

    KubernetesMockServer server;
    KubernetesClient client;

    @Test
    void restoresANodePoolAbandonedMidPlanAndLeavesARecentScaleDownAlone() {
        Instant startedAt = Instant.now();
        StrimziTestCluster cluster = new StrimziTestCluster(server, client)
                .pool("brokers-alpha", "broker", 3)
                .pool("brokers-sigma", "broker", 4)
                .pool("brokers-gamma", "broker", 5)
                .pool("brokers-delta", "broker", 6);
        cluster.scaledDown("brokers-sigma", 0);
        // A plan this process started.
        cluster.scaledDown("brokers-gamma", 0, startedAt.plusSeconds(1).toEpochMilli());
        // The pod was killed 2 minutes into the plan and came back 90 s later.
        cluster.scaledDown("brokers-delta", 0, startedAt.minusSeconds(120).toEpochMilli());

        ScaleDownSnapshots.Restored restored = reconciler().reconcileScaleDowns(startedAt);

        assertEquals(new ScaleDownSnapshots.Restored(0, 2), restored);
        assertEquals(1, cluster.replicas("brokers-sigma"));
        assertTrue(cluster.annotations("brokers-sigma").isEmpty(), "snapshot cleared");
        assertEquals(1, cluster.replicas("brokers-delta"));
        assertEquals(0, cluster.replicas("brokers-gamma"));
        assertEquals(1, cluster.replicas("brokers-alpha"));
    }

    @Test
    void deletesKatesNetworkPoliciesFromBeforeTheStartInEveryNamespace() {
        // The mock server stamps creationTimestamp itself, so the start moves
        // around the policies instead.
        policy("kafka", "kates-netpol-krafter-brokers-sigma-4", true);
        policy("kates", "kates-netpol-consumer", true);
        policy("kafka", "allow-brokers", false);
        DisruptionOrphanReconciler reconciler = reconciler();

        assertEquals(0, reconciler.reconcileNetworkPolicies(Instant.now().minusSeconds(60)));
        assertNotNull(get("kafka", "kates-netpol-krafter-brokers-sigma-4"), "made after the start");

        assertEquals(2, reconciler.reconcileNetworkPolicies(Instant.now().plusSeconds(60)));
        assertNull(get("kafka", "kates-netpol-krafter-brokers-sigma-4"));
        assertNull(get("kates", "kates-netpol-consumer"));
        assertNotNull(get("kafka", "allow-brokers"), "not Kates-managed");
    }

    @Test
    void aPolicyStampedInTheSecondTheProcessStartedIsKept() {
        NetworkPolicy created = policy("kafka", "kates-netpol-krafter-brokers-sigma-4", true);
        // Stamped 12:00:00Z by a plan this process launched at 12:00:00.700.
        Instant startedAt =
                Instant.parse(created.getMetadata().getCreationTimestamp()).plusMillis(700);

        assertEquals(0, reconciler().reconcileNetworkPolicies(startedAt));
        assertNotNull(get("kafka", "kates-netpol-krafter-brokers-sigma-4"));
    }

    private DisruptionOrphanReconciler reconciler() {
        DisruptionOrphanReconciler reconciler = new DisruptionOrphanReconciler();
        reconciler.kubeClient = client;
        reconciler.kafkaNamespace = StrimziTestCluster.NAMESPACE;
        reconciler.minAgeSec = 0;
        return reconciler;
    }

    private NetworkPolicy policy(String namespace, String name, boolean katesManaged) {
        return client.network()
                .networkPolicies()
                .inNamespace(namespace)
                .resource(new NetworkPolicyBuilder()
                        .withNewMetadata()
                        .withName(name)
                        .withNamespace(namespace)
                        .withLabels(katesManaged ? Map.of("managed-by", "kates") : Map.of())
                        .endMetadata()
                        .build())
                .create();
    }

    private NetworkPolicy get(String namespace, String name) {
        return client.network()
                .networkPolicies()
                .inNamespace(namespace)
                .withName(name)
                .get();
    }
}
