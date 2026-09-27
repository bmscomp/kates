package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.mock;

import java.io.IOException;
import java.io.InputStream;
import java.util.Map;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult.TaskStatus;
import com.bmscomp.kates.trogdor.TrogdorClient;
import com.bmscomp.kates.trogdor.spec.ConsumeBenchSpec;
import com.bmscomp.kates.trogdor.spec.ProduceBenchSpec;
import com.bmscomp.kates.trogdor.spec.RoundTripWorkloadSpec;

/**
 * What a task becomes on Trogdor, and what Trogdor's answer becomes.
 *
 * <p>A task's client settings were left out of its spec entirely, so a run on
 * this backend used the Kafka client's defaults whatever its request said.
 *
 * <p>A task's status was read from fields no Trogdor worker reports. The
 * fixtures under {@code trogdor/} are task states as the coordinator
 * serializes them, each holding its workload's {@code StatusData} as the
 * worker in apache/kafka's trogdor module writes it.
 */
class TrogdorBackendTest {

    private final TrogdorBackend backend = new TrogdorBackend(mock(TrogdorClient.class));

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

    /** The coordinator's startedMs in every fixture whose task started. */
    private static final long STARTED_MS = 1_790_000_000_250L;

    /** A poll long after any fixture's task finished: DONE is timed to doneMs, not to now. */
    private static final long AN_HOUR_LATER = STARTED_MS + 3_600_000;

    private static JsonNode taskState(String fixture) throws IOException {
        try (InputStream in = TrogdorBackendTest.class.getResourceAsStream("/trogdor/" + fixture)) {
            assertNotNull(in, fixture);
            return JSON.readTree(in);
        }
    }

    @Test
    void aProduceTaskReportsWhatItSentOverTheTimeItRan() throws IOException {
        BenchmarkStatus status = TrogdorBackend.fromTrogdorStatus(taskState("produce-done.json"), AN_HOUR_LATER);

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
        BenchmarkStatus status =
                TrogdorBackend.fromTrogdorStatus(taskState("produce-running.json"), STARTED_MS + 60_000);

        assertEquals(TaskStatus.RUNNING, status.getState());
        assertEquals(300_000, status.getRecordsProcessed());
        assertEquals(5_000, status.getThroughputRecordsPerSec(), 1e-9);
    }

    @Test
    void aConsumeTaskCountsWhatEveryConsumerReceived() throws IOException {
        BenchmarkStatus status = TrogdorBackend.fromTrogdorStatus(taskState("consume-done.json"), AN_HOUR_LATER);

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
        BenchmarkStatus status = TrogdorBackend.fromTrogdorStatus(taskState("round-trip-done.json"), AN_HOUR_LATER);

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
        BenchmarkStatus status = TrogdorBackend.fromTrogdorStatus(taskState("no-node-done.json"), AN_HOUR_LATER);

        assertEquals(TaskStatus.FAILED, status.getState(), "DONE with an error is a failure");
        assertEquals("Unable to find nodes for task: Unknown node names: ", status.getError());
        assertEquals(0, status.getRecordsProcessed());
        assertEquals(0, status.getThroughputRecordsPerSec(), "it has no startedMs");
    }
}
