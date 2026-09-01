package com.bmscomp.kates.it;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.time.Duration;
import java.util.Properties;
import java.util.function.BooleanSupplier;
import jakarta.inject.Inject;

import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import org.apache.kafka.clients.producer.KafkaProducer;
import org.apache.kafka.clients.producer.ProducerConfig;
import org.apache.kafka.clients.producer.ProducerRecord;
import org.apache.kafka.common.serialization.ByteArraySerializer;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.service.TestRunRepository;

/**
 * End-to-end engine lifecycle against a real broker: submit → produce → ack →
 * terminal state.
 *
 * <p>Guards the two wave-2 P0 fixes that unit tests structurally cannot reach,
 * because both are about what happens with a real broker and real time:
 *
 * <ul>
 *   <li><b>P0-1</b> — produce latency must be the broker ROUND-TRIP. It used to
 *       be measured at {@code send()} return, which only timed the accumulator
 *       buffer append and understated latency by orders of magnitude.
 *   <li><b>P0-2</b> — a run must reach a terminal state on its own. Nothing in
 *       this test calls {@code refreshStatus}; only the scheduled reconciler can
 *       move the run to DONE.
 * </ul>
 */
@QuarkusTest
@TestProfile(IntegrationTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
@QuarkusTestResource(value = KafkaTestResource.class, restrictToAnnotatedClass = true)
class EngineLifecycleIT {

    private static final int RECORDS = 25;

    @Inject
    TestOrchestrator orchestrator;

    @Inject
    TestRunRepository repository;

    @ConfigProperty(name = "kates.kafka.bootstrap-servers")
    String bootstrapServers;

    @Test
    void runReachesDoneViaReconcilerAndRecordsRoundTripLatency() {
        // Reference round-trip measured independently, so the assertion below
        // calibrates itself to the machine instead of hard-coding a millisecond
        // threshold that a fast or loaded CI box could invalidate.
        double referenceRttMs = measureReferenceRoundTripMs();
        assertTrue(referenceRttMs > 0, "reference round-trip should be measurable");

        TestRun submitted = submitVolumeRun();

        // NOTE: deliberately polls the REPOSITORY, never orchestrator.refreshStatus.
        // Before P0-2 nothing drove a finished run to DONE unless a client polled
        // it, so this loop would time out (and the reaper would eventually mark
        // the run FAILED instead).
        boolean terminal = waitUntil(
                Duration.ofSeconds(90),
                () -> repository
                        .findById(submitted.getId())
                        .map(r -> r.getStatus() == TestResult.TaskStatus.DONE
                                || r.getStatus() == TestResult.TaskStatus.FAILED)
                        .orElse(false));
        assertTrue(terminal, "the scheduled reconciler must drive the run to a terminal state without client polling");

        TestRun finished = repository.findById(submitted.getId()).orElseThrow();
        assertEquals(
                TestResult.TaskStatus.DONE,
                finished.getStatus(),
                "run should complete cleanly against the container broker");
        assertFalse(finished.getResults().isEmpty(), "a completed run carries its task results");

        TestResult produce = finished.getResults().stream()
                .filter(r -> r.getRecordsSent() > 0)
                .findFirst()
                .orElseThrow(() -> new AssertionError("no produce result recorded"));

        assertEquals(RECORDS, produce.getRecordsSent(), "every requested record was produced");

        // The P0-1 guard. Measuring at send() return yields microseconds — three
        // orders of magnitude below a real ack — so anything within the same
        // order as the reference round-trip proves the sample is taken in the
        // callback. The floor keeps the check meaningful if the reference is
        // implausibly fast.
        double floorMs = Math.max(0.1, referenceRttMs / 5.0);
        assertTrue(
                produce.getP50LatencyMs() >= floorMs,
                String.format(
                        "produce latency must be the broker round-trip, not the send() enqueue: "
                                + "p50=%.4fms, reference round-trip=%.4fms, floor=%.4fms",
                        produce.getP50LatencyMs(), referenceRttMs, floorMs));
        assertTrue(produce.getMaxLatencyMs() >= produce.getP50LatencyMs(), "max latency is at least the median");
    }

    @Test
    void concurrencyPermitIsHeldForTheRunAndReleasedOnCompletion() {
        // P1-5: the permit used to be released as soon as submission returned,
        // so activeTestCount() read ~0 while work was actually running and the
        // cap never applied.
        TestRun submitted = submitVolumeRun();

        boolean terminal = waitUntil(
                Duration.ofSeconds(90),
                () -> repository
                        .findById(submitted.getId())
                        .map(r -> r.getStatus() == TestResult.TaskStatus.DONE
                                || r.getStatus() == TestResult.TaskStatus.FAILED)
                        .orElse(false));
        assertTrue(terminal, "run reached a terminal state");

        // The slot must come back once the run is terminal, otherwise the engine
        // would refuse work forever after maxConcurrentTests runs.
        boolean released = waitUntil(Duration.ofSeconds(30), () -> orchestrator.activeTestCount() == 0);
        assertTrue(released, "the concurrency permit is returned on the terminal transition");
    }

    private TestRun submitVolumeRun() {
        TestSpec spec = new TestSpec();
        // VOLUME is producer-only, so the run finishes as soon as the records
        // are acked — no consumer holding it open for its full duration.
        spec.setTopic(ItSupport.uniqueTopic("engine-it"));
        spec.setNumRecords(RECORDS);
        spec.setRecordSize(512);
        spec.setPartitions(1);
        spec.setReplicationFactor(1); // single-broker container
        spec.setMinInsyncReplicas(1);
        spec.setAcks("all");
        spec.setDurationMs(60_000);

        CreateTestRequest request = new CreateTestRequest();
        request.setType(TestType.VOLUME);
        request.setSpec(spec);
        request.setBackend("native");

        var result = orchestrator.executeTest(request);
        assertFalse(
                result.isFailure(),
                "submission should succeed: "
                        + result.asFailure().map(Throwable::getMessage).orElse(""));
        return result.asSuccess().orElseThrow();
    }

    /** Times a single acked produce against the same broker. */
    private double measureReferenceRoundTripMs() {
        Properties props = new Properties();
        props.put(ProducerConfig.BOOTSTRAP_SERVERS_CONFIG, bootstrapServers);
        props.put(ProducerConfig.KEY_SERIALIZER_CLASS_CONFIG, ByteArraySerializer.class.getName());
        props.put(ProducerConfig.VALUE_SERIALIZER_CLASS_CONFIG, ByteArraySerializer.class.getName());
        props.put(ProducerConfig.ACKS_CONFIG, "all");
        props.put(ProducerConfig.LINGER_MS_CONFIG, "0");

        String topic = ItSupport.uniqueTopic("engine-it-reference");
        double best = Double.MAX_VALUE;
        try (KafkaProducer<byte[], byte[]> producer = new KafkaProducer<>(props)) {
            for (int i = 0; i < 3; i++) {
                long start = System.nanoTime();
                try {
                    producer.send(new ProducerRecord<>(topic, new byte[512])).get();
                } catch (Exception e) {
                    throw new AssertionError("reference produce failed", e);
                }
                best = Math.min(best, (System.nanoTime() - start) / 1_000_000.0);
            }
        }
        return best;
    }

    private static boolean waitUntil(Duration timeout, BooleanSupplier condition) {
        long deadline = System.nanoTime() + timeout.toNanos();
        while (System.nanoTime() < deadline) {
            if (condition.getAsBoolean()) {
                return true;
            }
            try {
                Thread.sleep(250);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                return false;
            }
        }
        return condition.getAsBoolean();
    }
}
