package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import java.util.HashMap;
import java.util.List;
import java.util.concurrent.TimeUnit;

import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;
import io.fabric8.kubernetes.client.utils.Serialization;
import io.fabric8.mockwebserver.Context;
import io.fabric8.mockwebserver.MockWebServer;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * A KafkaNodePool patch the API server carries out but whose answer is lost.
 * The step fails, and a failed scale-down gives the pools it lowered their
 * replicas back; this pool used to be left out, still lowered, for the Cluster
 * Operator to remove its broker in the middle of a later step.
 */
class NodePoolScaleDownLostAnswerTest {

    private static final String BROKERS = "/apis/kafka.strimzi.io/v1/namespaces/kafka/kafkanodepools/brokers";

    private final LossyDispatcher dispatcher = new LossyDispatcher();
    private KubernetesMockServer server;
    private KubernetesClient client;
    private KubernetesChaosProvider provider;

    @BeforeEach
    void start() {
        server = new KubernetesMockServer(
                new Context(Serialization.jsonMapper()), new MockWebServer(), new HashMap<>(), dispatcher, false);
        server.init();
        // No request sent again by the client itself, as in the application's
        // tests: the failure has to reach the provider, as it does once the
        // client's own retries run out.
        client = server.createClient(builder -> {
            new VertxPerMockClient().accept(builder);
            builder.editOrNewConfig().withRequestRetryBackoffLimit(0).endConfig();
        });
        provider = new KubernetesChaosProvider();
        provider.client = client;
        provider.self = provider;
        provider.executor = new com.bmscomp.kates.engine.KatesExecutor();
    }

    @AfterEach
    void stop() {
        client.close();
        server.destroy();
    }

    @Test
    void aLoweredPoolWhoseAnswerIsLostGetsItsReplicasBack() throws Exception {
        StrimziTestCluster cluster = new StrimziTestCluster(server, client)
                .pool("controllers", "controller", 0, 1, 2)
                .pool("brokers", "broker", 3, 4, 5);
        dispatcher.loseAnswerOnce("PATCH", BROKERS);

        ChaosOutcome outcome = provider.triggerFault(FaultSpec.builder("scale-down")
                        .targetLabel("strimzi.io/pool-name=brokers")
                        .disruptionType(DisruptionType.SCALE_DOWN)
                        .chaosDurationSec(0)
                        .build())
                .get(5, TimeUnit.SECONDS);

        assertFalse(outcome.isPass(), "the client never saw the pool lowered");
        assertEquals(3, cluster.replicas("brokers"));
        assertFalse(cluster.annotations("brokers").containsKey(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION));
        assertEquals(
                List.of("PATCH " + BROKERS, "PATCH " + BROKERS),
                dispatcher.writes().stream()
                        .filter(w -> w.equals("PATCH " + BROKERS))
                        .toList(),
                "lowered once, then put back");
    }

    @Test
    void aPoolThePatchNeverReachedIsLeftAsItWas() throws Exception {
        StrimziTestCluster cluster = new StrimziTestCluster(server, client)
                .pool("controllers", "controller", 0, 1, 2)
                .pool("brokers", "broker", 3, 4, 5, 6);
        // An earlier step took it from 4 to 3.
        cluster.scaledDown("brokers", 3);
        dispatcher.failOnce("PATCH", BROKERS);

        ChaosOutcome outcome = provider.triggerFault(FaultSpec.builder("scale-down")
                        .targetLabel("strimzi.io/pool-name=brokers")
                        .disruptionType(DisruptionType.SCALE_DOWN)
                        .chaosDurationSec(0)
                        .build())
                .get(5, TimeUnit.SECONDS);

        assertFalse(outcome.isPass());
        assertEquals(3, cluster.replicas("brokers"));
        assertEquals(
                "4",
                cluster.annotations("brokers").get(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION),
                "the earlier step's snapshot stays for rollback");
    }
}
