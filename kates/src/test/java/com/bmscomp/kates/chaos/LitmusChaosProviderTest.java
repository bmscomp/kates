package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import java.util.Arrays;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;

import io.fabric8.kubernetes.api.model.DeleteOptions;
import io.fabric8.kubernetes.api.model.PodBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.server.mock.EnableKubernetesMockClient;
import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;
import io.fabric8.mockwebserver.http.RecordedRequest;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.EnumSource;

import com.bmscomp.kates.chaos.litmus.ChaosEngine;
import com.bmscomp.kates.chaos.litmus.ChaosEngineSpec;
import com.bmscomp.kates.chaos.litmus.ChaosResult;
import com.bmscomp.kates.chaos.litmus.ChaosResultStatus;

/**
 * TARGET_PODS used to be one random pod whatever the spec asked for:
 * targetBrokerId was ignored and a zone-wide fault hit a single broker.
 */
@EnableKubernetesMockClient(crud = true, kubernetesClientBuilderCustomizer = VertxPerMockClient.class)
class LitmusChaosProviderTest {

    KubernetesMockServer server;
    KubernetesClient client;

    private LitmusChaosProvider provider;

    @BeforeEach
    void setup() {
        provider = new LitmusChaosProvider();
        provider.client = client;
        provider.executor = new com.bmscomp.kates.engine.KatesExecutor();
        provider.kubernetes = new KubernetesChaosProvider();
        provider.kubernetes.client = client;
        provider.kubernetes.self = provider.kubernetes;
        provider.kubernetes.executor = new com.bmscomp.kates.engine.KatesExecutor();
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
                            .addNewOwnerReference()
                            .withApiVersion("core.strimzi.io/v1")
                            .withKind("StrimziPodSet")
                            .withName("krafter-brokers")
                            .withUid("podset-uid")
                            .endOwnerReference()
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
    void rollingRestartGoesThroughTheStrimziOperatorNotPodDelete() throws Exception {
        // Used to run pod-delete with FORCE=true: one pod force-killed.
        FaultSpec spec = FaultSpec.builder("rolling-restart")
                .disruptionType(DisruptionType.ROLLING_RESTART)
                .chaosDurationSec(0)
                .build();

        ChaosOutcome outcome = provider.triggerFault(spec).get(5, TimeUnit.SECONDS);

        assertTrue(outcome.isPass(), outcome.failureReason());
        assertTrue(client.resources(ChaosEngine.class)
                .inNamespace("kafka")
                .list()
                .getItems()
                .isEmpty());
        for (String pod : List.of("krafter-brokers-0", "krafter-brokers-1", "krafter-brokers-2")) {
            assertEquals(
                    "true",
                    client.pods()
                            .inNamespace("kafka")
                            .withName(pod)
                            .get()
                            .getMetadata()
                            .getAnnotations()
                            .get(KubernetesChaosProvider.MANUAL_ROLLING_UPDATE_ANNOTATION),
                    pod);
        }
    }

    @Test
    void scaleDownLowersTheNodePoolInsteadOfDeletingAPod() throws Exception {
        // Used to run pod-delete with FORCE=true: one pod killed, and brought
        // straight back by its StrimziPodSet.
        StrimziTestCluster cluster = new StrimziTestCluster(server, client)
                .pool("controllers", "controller", 0, 1, 2)
                .pool("brokers", "broker", 3, 4, 5);
        FaultSpec spec = FaultSpec.builder("scale-down")
                .targetLabel("strimzi.io/pool-name=brokers")
                .disruptionType(DisruptionType.SCALE_DOWN)
                .chaosDurationSec(0)
                .build();

        ChaosOutcome outcome = provider.triggerFault(spec).get(5, TimeUnit.SECONDS);

        assertTrue(outcome.isPass(), outcome.failureReason());
        assertEquals(2, cluster.replicas("brokers"));
        assertTrue(client.resources(ChaosEngine.class)
                .inNamespace("kafka")
                .list()
                .getItems()
                .isEmpty());
    }

    @Test
    void nodeDrainTakesNoTargetPods() {
        FaultSpec spec = FaultSpec.builder("drain")
                .disruptionType(DisruptionType.NODE_DRAIN)
                .build();

        assertNull(env(provider.buildChaosEngine(spec, "engine", "node-drain"), "TARGET_PODS"));
    }

    /**
     * Only a cmdProbe becomes a Litmus probe, the one type the provider gives
     * inputs. Litmus has no kafkaProbe, and the k8sProbe "kafka Ready" is
     * Kates' own query: Kates evaluates both, and Litmus would fail them empty.
     */
    @Test
    void onlyCmdProbesBecomeLitmusProbes() {
        FaultSpec spec = FaultSpec.builder("probed")
                .targetBrokerId(2)
                .disruptionType(DisruptionType.POD_KILL)
                .probes(List.of(
                        KafkaProbes.isrHealth(),
                        KafkaProbes.clusterReady(),
                        ProbeSpec.builder("custom")
                                .command("echo 0")
                                .expectedOutput("0")
                                .build()))
                .build();

        List<ChaosEngineSpec.Probe> probes = build(spec).getSpec().experiments.getFirst().spec.probe;

        assertEquals(List.of("custom"), probes.stream().map(p -> p.name).toList());
        assertEquals("echo 0", probes.getFirst().cmdProbe.inputs.command);
    }

    @Test
    void aFaultWhoseProbesKatesEvaluatesSendsLitmusNone() {
        FaultSpec spec = FaultSpec.builder("probed")
                .targetBrokerId(2)
                .disruptionType(DisruptionType.POD_KILL)
                .probes(List.of(KafkaProbes.isrHealth(), KafkaProbes.clusterReady()))
                .build();

        assertNull(build(spec).getSpec().experiments.getFirst().spec.probe);
    }

    @Test
    void podDeleteDeletesThePodWithItsGracePeriod() throws Exception {
        // Used to run pod-delete with FORCE=true, a delete with no grace period
        // whatever gracePeriodSec said: no SIGTERM, no controlled shutdown.
        FaultSpec spec = FaultSpec.builder("graceful")
                .targetBrokerId(2)
                .disruptionType(DisruptionType.POD_DELETE)
                .gracePeriodSec(45)
                .build();

        ChaosOutcome outcome = provider.triggerFault(spec).get(5, TimeUnit.SECONDS);

        assertTrue(outcome.isPass(), outcome.failureReason());
        RecordedRequest delete = server.getLastRequest();
        assertEquals("DELETE", delete.getMethod());
        assertEquals("/api/v1/namespaces/kafka/pods/krafter-brokers-2", delete.getPath());
        DeleteOptions options =
                client.getKubernetesSerialization().unmarshal(delete.getUtf8Body(), DeleteOptions.class);
        assertEquals(45L, options.getGracePeriodSeconds());
        assertTrue(client.resources(ChaosEngine.class)
                .inNamespace("kafka")
                .list()
                .getItems()
                .isEmpty());
    }

    @ParameterizedTest
    @EnumSource(
            value = DisruptionType.class,
            names = {"POD_KILL", "LEADER_ELECTION"})
    void podDeleteExperimentStaysForcedAndSerial(DisruptionType type) {
        // Litmus fails its recovery check on pods of a StrimziPodSet without them.
        FaultSpec spec = FaultSpec.builder("kill").disruptionType(type).build();

        ChaosEngine engine = build(spec);

        assertEquals("true", env(engine, "FORCE"));
        assertEquals("serial", env(engine, "SEQUENCE"));
    }

    @Test
    void delayBeforeSecIsWaitedBeforeTheChaosEngineExists() throws Exception {
        // Used to be ignored: the ChaosEngine was created at once.
        provider.resultPollIntervalMs = 20;
        FaultSpec spec = FaultSpec.builder("delayed-kill")
                .targetBrokerId(2)
                .disruptionType(DisruptionType.POD_KILL)
                .delayBeforeSec(1)
                .build();
        long delayNanos = TimeUnit.SECONDS.toNanos(1);

        long triggered = System.nanoTime();
        CompletableFuture<ChaosOutcome> fault = provider.triggerFault(spec);
        ChaosEngine engine = awaitEngine();
        long seen = System.nanoTime();
        pass(engine.getMetadata().getName() + "-pod-delete");
        ChaosOutcome outcome = fault.get(5, TimeUnit.SECONDS);

        assertTrue(seen - triggered >= delayNanos, "the ChaosEngine existed before the delay was over");
        assertEquals("krafter-brokers-2", env(engine, "TARGET_PODS"));
        assertTrue(outcome.isPass(), outcome.failureReason());
        assertTrue(outcome.chaosStartNanos() - triggered >= delayNanos, "the fault's start includes the delay");
    }

    private ChaosEngine awaitEngine() throws InterruptedException {
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5);
        while (System.nanoTime() < deadline) {
            List<ChaosEngine> engines = client.resources(ChaosEngine.class)
                    .inNamespace("kafka")
                    .list()
                    .getItems();
            if (!engines.isEmpty()) {
                return engines.getFirst();
            }
            Thread.sleep(10);
        }
        return fail("no ChaosEngine was created");
    }

    /** Stands in for the Litmus chaos-runner: the experiment passed. */
    private void pass(String resultName) {
        ChaosResult result = new ChaosResult();
        result.getMetadata().setName(resultName);
        result.getMetadata().setNamespace("kafka");
        ChaosResultStatus status = new ChaosResultStatus();
        status.experimentStatus = new ChaosResultStatus.ExperimentStatus();
        status.experimentStatus.verdict = "Pass";
        result.setStatus(status);
        client.resources(ChaosResult.class)
                .inNamespace("kafka")
                .resource(result)
                .create();
    }
}
