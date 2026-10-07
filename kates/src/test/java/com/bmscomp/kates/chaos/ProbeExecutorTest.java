package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.verifyNoInteractions;
import static org.mockito.Mockito.when;

import java.time.Duration;
import java.util.List;
import java.util.Map;

import io.fabric8.kubernetes.api.model.GenericKubernetesResource;
import io.fabric8.kubernetes.api.model.ObjectMetaBuilder;
import io.fabric8.kubernetes.api.model.PodBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.server.mock.EnableKubernetesMockClient;
import io.fabric8.kubernetes.client.server.mock.ErrorStreamMessage;
import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;
import io.fabric8.kubernetes.client.server.mock.OutputStreamMessage;
import io.fabric8.kubernetes.client.server.mock.StatusStreamMessage;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.junit.jupiter.params.provider.ValueSource;

/**
 * ProbeExecutor fails a probe it cannot evaluate. It used to read any output
 * that was not a number as 0, compare with contains for a comparator it did
 * not know, ignore a command's exit code and timeout, and pass contains
 * "Ready" on a NotReady Kafka. A probe that could not run passed.
 */
@EnableKubernetesMockClient(crud = true, kubernetesClientBuilderCustomizer = VertxPerMockClient.class)
class ProbeExecutorTest {

    private static final String NAMESPACE = "kafka";

    KubernetesMockServer server;
    KubernetesClient client;

    private ProbeExecutor executor;
    private KafkaProbeChecks kafkaChecks;

    @BeforeEach
    void setup() {
        server.expectCustomResource(NodePoolScaleDown.KAFKAS);
        kafkaChecks = mock(KafkaProbeChecks.class);
        executor = new ProbeExecutor();
        executor.client = client;
        executor.kafkaChecks = kafkaChecks;
    }

    @ParameterizedTest
    @CsvSource({
        "hello world, world, contains, true",
        "hello world, xyz, contains, false",
        "exact match, exact match, equal, true",
        "exact match, different, equal, false",
        "hello world, xyz, notContains, true",
        "hello world, world, notContains, false",
        "50, 100, <=, true",
        "100, 100, <=, true",
        "101, 100, <=, false",
        "50, 100, >=, false",
        "150, 100, >=, true",
        "50, 100, <, true",
        "100, 100, <, false",
        "101, 100, >, true",
        "52.91, 0, >, true",
        "1e3, 999, >, true",
        "-1, 0, <, true",
        "0, 0, ==, true",
        "0.0, 0, ==, true",
        // == used to compare as contains, and "10" contains "0".
        "10, 0, ==, false",
        "10, 0, !=, true",
        "0, 0, !=, false"
    })
    void comparators(String actual, String expected, String comparator, boolean passes) {
        assertEquals(
                passes,
                ProbeExecutor.compare(actual, expected, comparator),
                actual + " " + comparator + " " + expected);
    }

    /**
     * What the old commands printed when they could not reach a broker
     * ({@code grep -c} and {@code echo '0'} both print 0), an error message,
     * and the producer probe's MB/s field. Each used to read as a number.
     */
    @ParameterizedTest
    @ValueSource(
            strings = {"", "0\n0", "No Kafka pods found in namespace kafka-1", "(0.01", "NaN", "Infinity", "1,000"})
    void numericComparatorFailsOnOutputThatIsNotOneNumber(String output) {
        ProbeFailure failure = assertThrows(ProbeFailure.class, () -> ProbeExecutor.compare(output, "50", "<="));

        assertTrue(failure.getMessage().startsWith("output is not a number: '"), failure.getMessage());
    }

    @Test
    void unknownComparatorFailsTheProbeBeforeAnythingRuns() {
        ProbeSpec probe = kafkaProbe("unavailable-partitions", "0", "=~");

        ProbeResult result = executor.evaluate(probe, NAMESPACE);

        assertFalse(result.passed());
        assertTrue(result.output().startsWith("Error: comparator '=~' is not one of ["), result.output());
        verifyNoInteractions(kafkaChecks);
    }

    @Test
    void numericComparatorNeedsANumberToCompareWith() {
        // expectedOutput defaults to "".
        ProbeSpec probe = ProbeSpec.builder("p")
                .type("kafkaProbe")
                .command("unavailable-partitions")
                .comparator("<=")
                .build();

        ProbeResult result = executor.evaluate(probe, NAMESPACE);

        assertFalse(result.passed());
        assertEquals("Error: expectedOutput is not a number: ''", result.output());
        verifyNoInteractions(kafkaChecks);
    }

    @Test
    void kafkaProbeWhoseCommandNamesNoCheckFailsBeforeAnythingRuns() {
        ProbeResult result = executor.evaluate(
                kafkaProbe("kafka-topics.sh --describe --under-replicated-partitions", "0", "<="), NAMESPACE);

        assertFalse(result.passed());
        assertTrue(result.output().startsWith("Error: a kafkaProbe has no check 'kafka-topics.sh'"), result.output());
        verifyNoInteractions(kafkaChecks);
    }

    @Test
    void probeWithoutExpectedOutputFails() {
        ProbeResult result = executor.evaluate(kafkaProbe("unavailable-partitions", null, "contains"), NAMESPACE);

        assertFalse(result.passed());
        assertEquals("Error: the probe has no expectedOutput to compare with", result.output());
    }

    @Test
    void kafkaProbeRunsItsCheckWithinTheProbesTimeout() {
        when(kafkaChecks.run("under-replicated-partitions", Duration.ofSeconds(30)))
                .thenReturn("3");

        ProbeResult result = executor.evaluate(KafkaProbes.isrHealth(), NAMESPACE);

        assertTrue(result.passed());
        assertEquals("3", result.output());
    }

    @Test
    void kafkaProbeOverItsThresholdFailsWithItsOutput() {
        when(kafkaChecks.run(anyString(), any())).thenReturn("51");

        ProbeResult result = executor.evaluate(KafkaProbes.isrHealth(), NAMESPACE);

        assertFalse(result.passed());
        assertEquals("51", result.output());
    }

    @Test
    void kafkaProbeThatCannotAskFails() {
        when(kafkaChecks.run(anyString(), any()))
                .thenThrow(new ProbeFailure("listing topics failed: TimeoutException: Timed out waiting for a node"));

        ProbeResult result = executor.evaluate(KafkaProbes.partitionAvailability(), NAMESPACE);

        assertFalse(result.passed());
        assertEquals("Error: listing topics failed: TimeoutException: Timed out waiting for a node", result.output());
    }

    @Test
    void clusterReadyPassesWhenTheReadyConditionIsTrue() {
        kafka(Map.of("conditions", List.of(Map.of("type", "Ready", "status", "True"))));

        ProbeResult result = executor.evaluate(KafkaProbes.clusterReady(), NAMESPACE);

        assertTrue(result.passed(), result.output());
        assertEquals("Ready=True", result.output());
    }

    @Test
    void clusterReadyFailsOnANotReadyCondition() {
        kafka(Map.of(
                "conditions",
                List.of(Map.of(
                        "type", "NotReady",
                        "status", "True",
                        "reason", "Creating",
                        "message", "Kafka cluster is being deployed"))));

        ProbeResult result = executor.evaluate(KafkaProbes.clusterReady(), NAMESPACE);

        assertFalse(result.passed());
        assertEquals("NotReady=True (Creating: Kafka cluster is being deployed)", result.output());
        // What the old probe compared it with: contains "Ready" passed.
        assertTrue(result.output().contains("Ready"));
    }

    @Test
    void readinessNamesTheReadyConditionWhenItIsNotTrue() {
        assertEquals(
                "Ready=False (KafkaUnavailable)",
                ProbeExecutor.readiness(Map.of(
                        "conditions",
                        List.of(
                                Map.of("type", "Warning", "status", "True", "reason", "KafkaStorage"),
                                Map.of("type", "Ready", "status", "False", "reason", "KafkaUnavailable")))));
    }

    @Test
    void aWarningBesideReadyStillReadsReady() {
        assertEquals(
                "Ready=True",
                ProbeExecutor.readiness(Map.of(
                        "conditions",
                        List.of(
                                Map.of("type", "Warning", "status", "True", "reason", "KafkaStorage"),
                                Map.of("type", "Ready", "status", "True")))));
    }

    @Test
    void aKafkaWithoutConditionsIsNotReady() {
        assertEquals("no status conditions", ProbeExecutor.readiness(null));
        assertEquals("no status conditions", ProbeExecutor.readiness(Map.of("listeners", List.of())));
    }

    @Test
    void clusterReadyFailsWithoutAKafkaResource() {
        ProbeResult result = executor.evaluate(KafkaProbes.clusterReady(), NAMESPACE);

        assertFalse(result.passed());
        assertEquals("Error: no Kafka resource in namespace kafka", result.output());
    }

    @Test
    void cmdProbeRunsInTheFirstReadyBrokerThatIsNotBeingDeleted() {
        pod("krafter-a-controllers-3", false, true);
        pod("krafter-brokers-0", true, false);
        pod("krafter-brokers-1", true, true);
        pod("krafter-brokers-2", true, true);
        client.pods().inNamespace(NAMESPACE).withName("krafter-brokers-1").delete();

        assertEquals("krafter-brokers-2", executor.probePod(NAMESPACE));
    }

    @Test
    void cmdProbeWithoutAReadyBrokerFails() {
        pod("krafter-controllers-3", false, true);
        pod("krafter-brokers-0", true, false);

        ProbeResult result = executor.evaluate(cmdProbe(30), NAMESPACE);

        assertFalse(result.passed());
        assertEquals("Error: no Ready broker pod (strimzi.io/broker-role=true) in namespace kafka", result.output());
    }

    @Test
    void cmdProbeComparesTheOutputOfACommandThatExitedZero() {
        pod("krafter-brokers-0", true, true);
        exec("krafter-brokers-0", new OutputStreamMessage("2\n"), new StatusStreamMessage(0));

        ProbeResult result = executor.evaluate(cmdProbe(30), NAMESPACE);

        assertTrue(result.passed(), result.output());
        assertEquals("2", result.output());
    }

    /** kafka-topics.sh … | grep -c 'Topic:' || echo '0' printed 0 when the CLI failed, and <= passed it. */
    @Test
    void cmdProbeWhoseCommandExitsNonZeroFailsWhateverItPrinted() {
        pod("krafter-brokers-0", true, true);
        exec(
                "krafter-brokers-0",
                new OutputStreamMessage("0\n"),
                new ErrorStreamMessage("Timed out waiting for a node assignment."),
                new StatusStreamMessage(1));

        ProbeResult result = executor.evaluate(cmdProbe(30), NAMESPACE);

        assertFalse(result.passed());
        assertEquals(
                "Error: the command in pod krafter-brokers-0 exited 1: Timed out waiting for a node assignment.",
                result.output());
    }

    /** The old executor ignored its wait timing out and compared what had arrived, often nothing, read as 0. */
    @Test
    void cmdProbeThatOutlastsItsTimeoutFails() {
        pod("krafter-brokers-0", true, true);
        server.expect()
                .get()
                .withPath(execPath("krafter-brokers-0"))
                .andUpgradeToWebSocket()
                .open(new OutputStreamMessage("0\n"))
                // A String takes waitFor's delay (a ready-made message would
                // not), and a message still pending keeps the stream open: the
                // command is still running when the probe gives up on it.
                .waitFor(5_000)
                .andEmit("still running")
                .done()
                .always();

        ProbeResult result = executor.evaluate(cmdProbe(1), NAMESPACE);

        assertFalse(result.passed());
        assertEquals("Error: the command in pod krafter-brokers-0 did not finish within 1 s", result.output());
    }

    /** A pod that restarts mid-command closes the stream with no exit status. */
    @Test
    void cmdProbeThatEndsWithoutAnExitStatusFails() {
        pod("krafter-brokers-0", true, true);
        exec("krafter-brokers-0", new OutputStreamMessage("0\n"));

        ProbeResult result = executor.evaluate(cmdProbe(30), NAMESPACE);

        assertFalse(result.passed());
        assertEquals("Error: the command in pod krafter-brokers-0 ended without an exit status", result.output());
    }

    private static ProbeSpec kafkaProbe(String command, String expected, String comparator) {
        return ProbeSpec.builder("p")
                .type("kafkaProbe")
                .command(command)
                .expectedOutput(expected)
                .comparator(comparator)
                .build();
    }

    /** A cmdProbe whose one-word command keeps the exec URL free of escapes. */
    private static ProbeSpec cmdProbe(int timeoutSec) {
        return ProbeSpec.builder("count")
                .command("count")
                .expectedOutput("5")
                .comparator("<=")
                .timeoutSec(timeoutSec)
                .build();
    }

    private void kafka(Map<String, Object> status) {
        GenericKubernetesResource kafka = new GenericKubernetesResource();
        kafka.setApiVersion("kafka.strimzi.io/v1");
        kafka.setKind("Kafka");
        kafka.setMetadata(new ObjectMetaBuilder()
                .withName("krafter")
                .withNamespace(NAMESPACE)
                .build());
        kafka.setAdditionalProperty("status", status);
        client.genericKubernetesResources(NodePoolScaleDown.KAFKAS)
                .inNamespace(NAMESPACE)
                .resource(kafka)
                .create();
    }

    /** A Kafka pod; a finalizer keeps it, marked for deletion, when a test deletes it. */
    private void pod(String name, boolean broker, boolean ready) {
        client.pods()
                .inNamespace(NAMESPACE)
                .resource(new PodBuilder()
                        .withNewMetadata()
                        .withName(name)
                        .withNamespace(NAMESPACE)
                        .addToLabels("strimzi.io/cluster", "krafter")
                        .addToLabels("strimzi.io/component-type", "kafka")
                        .addToLabels("strimzi.io/broker-role", String.valueOf(broker))
                        .addToLabels("strimzi.io/controller-role", String.valueOf(!broker))
                        .addToFinalizers("kates.io/test")
                        .endMetadata()
                        .withNewSpec()
                        .addNewContainer()
                        .withName("kafka")
                        .endContainer()
                        .endSpec()
                        .withNewStatus()
                        .withPhase("Running")
                        .addNewCondition()
                        .withType("Ready")
                        .withStatus(ready ? "True" : "False")
                        .endCondition()
                        .endStatus()
                        .build())
                .create();
    }

    /** Answers the exec of {@link #cmdProbe} with {@code messages}, then closes the stream. */
    private void exec(String pod, Object... messages) {
        server.expect()
                .get()
                .withPath(execPath(pod))
                .andUpgradeToWebSocket()
                .open(messages)
                .done()
                .always();
    }

    private static String execPath(String pod) {
        return "/api/v1/namespaces/kafka/pods/" + pod
                + "/exec?command=sh&command=-c&command=count&container=kafka&stdout=true&stderr=true";
    }
}
