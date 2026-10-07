package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

import java.io.IOException;
import java.io.InputStream;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import jakarta.ws.rs.WebApplicationException;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult.TaskStatus;
import com.bmscomp.kates.trogdor.TrogdorClient;
import com.bmscomp.kates.trogdor.spec.ConsumeBenchSpec;
import com.bmscomp.kates.trogdor.spec.ProduceBenchSpec;
import com.bmscomp.kates.trogdor.spec.RoundTripWorkloadSpec;
import com.bmscomp.kates.trogdor.spec.TrogdorSpec;

/**
 * What a task becomes on Trogdor, and what Trogdor's answer becomes.
 *
 * <p>A task's client settings were left out of its spec entirely, so a run on
 * this backend used the Kafka client's defaults whatever its request said.
 *
 * <p>No spec could have run: the coordinator refused every one for a property
 * Trogdor does not declare, a payload generator without a type or a consume
 * topic map, and a spec that got past that named no agent. The
 * {@code *-spec.json} fixtures are specs as a Trogdor coordinator reads them.
 *
 * <p>A task's status was read from fields no Trogdor worker reports. The
 * other fixtures under {@code trogdor/} are task states as the coordinator
 * serializes them, each holding its workload's {@code StatusData} as the
 * worker in apache/kafka's trogdor module writes it.
 */
class TrogdorBackendTest {

    private final TrogdorClient client = mock(TrogdorClient.class);
    private final TrogdorBackend backend = new TrogdorBackend(client, List.of("agent-0"));

    private static final Map<String, String> PRODUCER = Map.of(
            "bootstrap.servers", "broker:9092",
            "acks", "all",
            "compression.type", "zstd",
            "enable.idempotence", "false");

    @Test
    void aProduceTaskCarriesItsProducerSettings() {
        BenchmarkTask task = BenchmarkTask.builder("t-produce", BenchmarkTask.WorkloadType.PRODUCE)
                .producerConfig(PRODUCER)
                .targetMessagesPerSec(750)
                .build();

        ProduceBenchSpec spec = (ProduceBenchSpec) backend.toTrogdorSpec(task);

        assertEquals("broker:9092", spec.getBootstrapServers());
        assertEquals(750, spec.getTargetMessagesPerSec());
        assertEquals(
                Map.of("acks", "all", "compression.type", "zstd", "enable.idempotence", "false"),
                spec.getProducerConf(),
                "the bootstrap servers have their own field");
    }

    @Test
    void aConsumeTaskCarriesItsGroupAndFetchSettings() {
        BenchmarkTask task = BenchmarkTask.builder("t-consume", BenchmarkTask.WorkloadType.CONSUME)
                .consumerGroup("perf-cg")
                .consumerConfig(Map.of("fetch.min.bytes", "65536", "fetch.max.wait.ms", "100"))
                .build();

        ConsumeBenchSpec spec = (ConsumeBenchSpec) backend.toTrogdorSpec(task);

        assertEquals("perf-cg", spec.getConsumerGroup());
        assertEquals(Map.of("fetch.min.bytes", "65536", "fetch.max.wait.ms", "100"), spec.getConsumerConf());
    }

    @Test
    void aRoundTripTaskCarriesBoth() {
        BenchmarkTask task = BenchmarkTask.builder("t-rt", BenchmarkTask.WorkloadType.ROUND_TRIP)
                .producerConfig(PRODUCER)
                .build();

        RoundTripWorkloadSpec spec = (RoundTripWorkloadSpec) backend.toTrogdorSpec(task);

        assertEquals("false", spec.getProducerConf().get("enable.idempotence"));
        assertFalse(spec.getProducerConf().containsKey("bootstrap.servers"));
        assertTrue(spec.getConsumerConf().isEmpty());
    }

    private static final ObjectMapper JSON = new ObjectMapper();

    private static JsonNode fixture(String name) throws IOException {
        try (InputStream in = TrogdorBackendTest.class.getResourceAsStream("/trogdor/" + name)) {
            assertNotNull(in, name);
            return JSON.readTree(in);
        }
    }

    /** The startMs in every spec fixture; a spec's own is the time it was built. */
    private static final long SPEC_START_MS = 1_790_000_000_000L;

    /** A spec as its JSON reads back, so numbers compare by value, not by Java type. */
    private static JsonNode asSent(TrogdorSpec spec) throws IOException {
        spec.setStartMs(SPEC_START_MS);
        return JSON.readTree(JSON.writeValueAsString(spec));
    }

    private static final Map<String, String> BROKER_ACKS_ALL =
            Map.of("bootstrap.servers", "broker:9092", "acks", "all");

    @Test
    void aProduceTaskIsTheSpecTrogdorReads() throws IOException {
        BenchmarkTask task = BenchmarkTask.builder("t-produce", BenchmarkTask.WorkloadType.PRODUCE)
                .topic("perf")
                .partitions(6)
                .targetMessagesPerSec(750)
                .maxMessages(600_000)
                .durationMs(120_000)
                .recordSize(2048)
                .producerConfig(BROKER_ACKS_ALL)
                .build();

        // One topic of six partitions, where "perf[0-5]" made six topics; a
        // typed value generator; no totalProducers; the agent.
        assertEquals(fixture("produce-spec.json"), asSent(backend.toTrogdorSpec(task)));
    }

    @Test
    void aConsumeTaskIsTheSpecTrogdorReads() throws IOException {
        BenchmarkTask task = BenchmarkTask.builder("t-consume", BenchmarkTask.WorkloadType.CONSUME)
                .topic("perf")
                .maxMessages(600_000)
                .durationMs(120_000)
                .consumerGroup("perf-cg")
                .producerConfig(Map.of("bootstrap.servers", "broker:9092"))
                .consumerConfig(Map.of("fetch.min.bytes", "65536"))
                .build();

        // activeTopics is a list of names, subscribed to through the group.
        assertEquals(fixture("consume-spec.json"), asSent(backend.toTrogdorSpec(task)));
    }

    @Test
    void aRoundTripTaskIsTheSpecTrogdorReads() throws IOException {
        BenchmarkTask task = BenchmarkTask.builder("t-rt", BenchmarkTask.WorkloadType.ROUND_TRIP)
                .topic("perf")
                .partitions(6)
                .maxMessages(50_000)
                .durationMs(60_000)
                .recordSize(512)
                .producerConfig(BROKER_ACKS_ALL)
                .build();

        // A value generator where valueSize was, and no rate limit as the
        // largest rate: RoundTrip aborts on the -1 Kates means it by.
        assertEquals(fixture("round-trip-spec.json"), asSent(backend.toTrogdorSpec(task)));
    }

    @Test
    void aTaskWithAStartTimeStartsThen() {
        // A scenario's later phase: the coordinator holds the task PENDING
        // until startMs, and its agent stops it at startMs plus durationMs.
        // Every spec used to start when it was built, so every phase ran at once.
        long startAtMs = System.currentTimeMillis() + 90_000;
        for (BenchmarkTask.WorkloadType type : List.of(
                BenchmarkTask.WorkloadType.PRODUCE,
                BenchmarkTask.WorkloadType.CONSUME,
                BenchmarkTask.WorkloadType.ROUND_TRIP)) {
            BenchmarkTask task = BenchmarkTask.builder("t-later-" + type, type)
                    .producerConfig(PRODUCER)
                    .durationMs(60_000)
                    .startAtMs(startAtMs)
                    .build();

            TrogdorSpec spec = backend.toTrogdorSpec(task);

            assertEquals(startAtMs, spec.getStartMs(), type.name());
            assertEquals(60_000, spec.getDurationMs(), type.name());
        }

        long before = System.currentTimeMillis();
        TrogdorSpec now = backend.toTrogdorSpec(BenchmarkTask.builder("t-now", BenchmarkTask.WorkloadType.PRODUCE)
                .producerConfig(PRODUCER)
                .build());
        assertTrue(
                now.getStartMs() >= before && now.getStartMs() <= System.currentTimeMillis(),
                "a task without a start time starts when its spec is built");
    }

    /**
     * The properties each spec's {@code @JsonCreator} takes in apache/kafka's
     * trogdor module (trunk; the same in 3.9). The coordinator reads a spec
     * with unknown properties refused, so anything else fails the task's
     * creation.
     */
    private static final Map<String, Set<String>> TROGDOR_PROPERTIES = Map.of(
            ProduceBenchSpec.CLASS_NAME,
            Set.of(
                    "class",
                    "startMs",
                    "durationMs",
                    "producerNode",
                    "bootstrapServers",
                    "targetMessagesPerSec",
                    "maxMessages",
                    "keyGenerator",
                    "valueGenerator",
                    "transactionGenerator",
                    "producerConf",
                    "commonClientConf",
                    "adminClientConf",
                    "activeTopics",
                    "inactiveTopics",
                    "useConfiguredPartitioner",
                    "skipFlush"),
            ConsumeBenchSpec.CLASS_NAME,
            Set.of(
                    "class",
                    "startMs",
                    "durationMs",
                    "consumerNode",
                    "bootstrapServers",
                    "targetMessagesPerSec",
                    "maxMessages",
                    "consumerGroup",
                    "consumerConf",
                    "commonClientConf",
                    "adminClientConf",
                    "threadsPerWorker",
                    "recordProcessor",
                    "activeTopics"),
            RoundTripWorkloadSpec.CLASS_NAME,
            Set.of(
                    "class",
                    "startMs",
                    "durationMs",
                    "clientNode",
                    "bootstrapServers",
                    "commonClientConf",
                    "adminClientConf",
                    "consumerConf",
                    "producerConf",
                    "targetMessagesPerSec",
                    "valueGenerator",
                    "activeTopics",
                    "maxMessages"));

    @Test
    void theFixturesHoldOnlyPropertiesTrogdorDeclares() throws IOException {
        for (String name : List.of("produce-spec.json", "consume-spec.json", "round-trip-spec.json")) {
            JsonNode spec = fixture(name);
            Set<String> sent = new HashSet<>();
            spec.fieldNames().forEachRemaining(sent::add);
            Set<String> unknown = new HashSet<>(sent);
            unknown.removeAll(TROGDOR_PROPERTIES.get(spec.path("class").asText()));
            assertEquals(Set.of(), unknown, name);
        }
    }

    @Test
    void tasksTakeTheAgentsInTurn() {
        TrogdorBackend twoAgents = new TrogdorBackend(client, List.of("agent-0", "agent-1"));
        BenchmarkTask produce =
                BenchmarkTask.builder("p", BenchmarkTask.WorkloadType.PRODUCE).build();
        BenchmarkTask consume =
                BenchmarkTask.builder("c", BenchmarkTask.WorkloadType.CONSUME).build();
        BenchmarkTask roundTrip = BenchmarkTask.builder("r", BenchmarkTask.WorkloadType.ROUND_TRIP)
                .build();

        assertEquals("agent-0", ((ProduceBenchSpec) twoAgents.toTrogdorSpec(produce)).getProducerNode());
        assertEquals("agent-1", ((ConsumeBenchSpec) twoAgents.toTrogdorSpec(consume)).getConsumerNode());
        assertEquals("agent-0", ((RoundTripWorkloadSpec) twoAgents.toTrogdorSpec(roundTrip)).getClientNode());
    }

    @Test
    void anUnlimitedProduceRateIsNotTenRecordsASecond() {
        BenchmarkTask task = BenchmarkTask.builder("t-produce", BenchmarkTask.WorkloadType.PRODUCE)
                .targetMessagesPerSec(-1)
                .build();

        ProduceBenchSpec spec = (ProduceBenchSpec) backend.toTrogdorSpec(task);

        // ProduceBench raises -1 to one record per 100 ms period.
        assertEquals(Integer.MAX_VALUE, spec.getTargetMessagesPerSec());
    }

    @Test
    void aTaskTheCoordinatorDoesNotKnowHasFailed() {
        when(client.getTask("t-gone")).thenThrow(new WebApplicationException(404));

        BenchmarkStatus status = backend.poll(new BenchmarkHandle("trogdor", "t-gone"));

        assertEquals(TaskStatus.FAILED, status.getState(), "it used to stay RUNNING until the reaper");
        assertEquals("The Trogdor coordinator has no task t-gone", status.getError());
    }

    @Test
    void aCoordinatorThatCannotAnswerLeavesTheTaskRunning() {
        when(client.getTask("t-busy")).thenThrow(new WebApplicationException(503));

        assertEquals(
                TaskStatus.RUNNING,
                backend.poll(new BenchmarkHandle("trogdor", "t-busy")).getState());
    }

    @Test
    void stopSendsTheTaskIdInTheBody() {
        backend.stop(new BenchmarkHandle("trogdor", "t-stop"));

        verify(client).stopTask(new TrogdorClient.StopTaskRequest("t-stop"));
    }

    /** The coordinator's startedMs in every fixture whose task started. */
    private static final long STARTED_MS = 1_790_000_000_250L;

    /** A poll long after any fixture's task finished: DONE is timed to doneMs, not to now. */
    private static final long AN_HOUR_LATER = STARTED_MS + 3_600_000;

    @Test
    void aProduceTaskReportsWhatItSentOverTheTimeItRan() throws IOException {
        BenchmarkStatus status = TrogdorBackend.fromTrogdorStatus(fixture("produce-done.json"), AN_HOUR_LATER);

        assertEquals(TaskStatus.DONE, status.getState());
        assertNull(status.getError());
        assertEquals(600_000, status.getRecordsProcessed());
        assertEquals(
                10_000,
                status.getThroughputRecordsPerSec(),
                1e-9,
                "600000 records between startedMs and doneMs, 60 s apart, not the count itself");
        assertEquals(3.42, status.getAvgLatencyMs(), 1e-9);
        assertEquals(2, status.getP50LatencyMs());
        assertEquals(9, status.getP95LatencyMs());
        assertEquals(21, status.getP99LatencyMs());
        assertEquals(0, status.getMaxLatencyMs(), "ProduceBench reports no maximum");
    }

    @Test
    void aRunningTaskIsTimedToThePoll() throws IOException {
        BenchmarkStatus status = TrogdorBackend.fromTrogdorStatus(fixture("produce-running.json"), STARTED_MS + 60_000);

        assertEquals(TaskStatus.RUNNING, status.getState());
        assertEquals(300_000, status.getRecordsProcessed());
        assertEquals(5_000, status.getThroughputRecordsPerSec(), 1e-9);
    }

    @Test
    void aConsumeTaskCountsWhatEveryConsumerReceived() throws IOException {
        BenchmarkStatus status = TrogdorBackend.fromTrogdorStatus(fixture("consume-done.json"), AN_HOUR_LATER);

        assertEquals(TaskStatus.DONE, status.getState());
        assertEquals(550_000, status.getRecordsProcessed(), "both consumers' totalMessagesReceived");
        assertEquals(5_000, status.getThroughputRecordsPerSec(), 1e-9, "550000 records over 110 s");
        // Its latency figures are the time between poll batches.
        assertEquals(0, status.getAvgLatencyMs());
        assertEquals(0, status.getP50LatencyMs());
        assertEquals(0, status.getP95LatencyMs());
        assertEquals(0, status.getP99LatencyMs());
        assertEquals(0, status.getMaxLatencyMs());
    }

    @Test
    void aRoundTripTaskCountsTheRecordsThatCameBack() throws IOException {
        BenchmarkStatus status = TrogdorBackend.fromTrogdorStatus(fixture("round-trip-done.json"), AN_HOUR_LATER);

        assertEquals(TaskStatus.DONE, status.getState());
        assertEquals(48_000, status.getRecordsProcessed(), "totalReceived, not totalUniqueSent");
        assertEquals(2_000, status.getThroughputRecordsPerSec(), 1e-9, "48000 records over 24 s");
        assertEquals(0, status.getAvgLatencyMs(), "RoundTrip reports no latency");
        assertEquals(0, status.getP50LatencyMs());
        assertEquals(0, status.getP95LatencyMs());
        assertEquals(0, status.getP99LatencyMs());
        assertEquals(0, status.getMaxLatencyMs());
    }

    @Test
    void aTaskThatCouldNotStartFailsWithTheCoordinatorsError() throws IOException {
        BenchmarkStatus status = TrogdorBackend.fromTrogdorStatus(fixture("no-node-done.json"), AN_HOUR_LATER);

        assertEquals(TaskStatus.FAILED, status.getState(), "DONE with an error is a failure");
        assertEquals("Unable to find nodes for task: Unknown node names: ", status.getError());
        assertEquals(0, status.getRecordsProcessed());
        assertEquals(0, status.getThroughputRecordsPerSec(), "it has no startedMs");
    }
}
