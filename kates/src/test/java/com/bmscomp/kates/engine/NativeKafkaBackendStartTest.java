package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;

import java.util.Optional;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.locks.LockSupport;

import org.apache.kafka.clients.producer.MockProducer;
import org.apache.kafka.common.serialization.ByteArraySerializer;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.config.KafkaSecurityConfig;
import com.bmscomp.kates.domain.TestResult.TaskStatus;

/**
 * When a native task starts. A scenario submits every phase at once, each
 * with the time it is to start, and its worker waits for that time before it
 * makes a client. Every worker used to start on submission, so a scenario's
 * phases, and a RAMP phase's steps, all ran at once.
 */
class NativeKafkaBackendStartTest {

    /** Unique per task: the latency gauges are tagged with the task id. */
    private static final AtomicInteger SEQ = new AtomicInteger();

    private final AtomicInteger producersMade = new AtomicInteger();
    private final AtomicLong madeAtMs = new AtomicLong();

    private final NativeKafkaBackend backend = new NativeKafkaBackend(
            "localhost:9092",
            plaintext(),
            null,
            props -> {
                producersMade.incrementAndGet();
                madeAtMs.set(System.currentTimeMillis());
                return new MockProducer<>(true, new ByteArraySerializer(), new ByteArraySerializer());
            },
            props -> {
                throw new AssertionError("a produce task makes no consumer");
            });

    private static KafkaSecurityConfig plaintext() {
        return new KafkaSecurityConfig(
                "PLAINTEXT",
                Optional.empty(),
                Optional.empty(),
                Optional.empty(),
                Optional.empty(),
                Optional.empty(),
                Optional.empty(),
                Optional.empty(),
                Optional.empty(),
                Optional.empty(),
                Optional.empty());
    }

    private static BenchmarkTask produceAt(long startAtMs) {
        return BenchmarkTask.builder("start-" + SEQ.incrementAndGet(), BenchmarkTask.WorkloadType.PRODUCE)
                .runId("run-start")
                .topic("load-test")
                .maxMessages(10)
                .durationMs(30_000)
                .recordSize(64)
                .startAtMs(startAtMs)
                .build();
    }

    private BenchmarkStatus awaitTerminal(BenchmarkHandle handle) {
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(20);
        BenchmarkStatus status = backend.poll(handle);
        while (!status.isTerminal()) {
            assertTrue(System.nanoTime() < deadline, "the task did not finish: " + status.getState());
            LockSupport.parkNanos(TimeUnit.MILLISECONDS.toNanos(20));
            status = backend.poll(handle);
        }
        return status;
    }

    @Test
    void aTaskWaitsPendingUntilItsStartTime() {
        long startAtMs = System.currentTimeMillis() + 1_000;

        BenchmarkHandle handle = backend.submit(produceAt(startAtMs));

        BenchmarkStatus waiting = backend.poll(handle);
        assertEquals(TaskStatus.PENDING, waiting.getState());
        assertEquals(0, waiting.getRecordsProcessed());
        assertEquals(0, producersMade.get(), "no client before its turn");

        BenchmarkStatus done = awaitTerminal(handle);
        assertEquals(TaskStatus.DONE, done.getState(), done.getError());
        assertEquals(10, done.getRecordsProcessed());
        assertTrue(madeAtMs.get() >= startAtMs, "its producer was made " + (startAtMs - madeAtMs.get()) + " ms early");
    }

    @Test
    void aTaskStoppedBeforeItsTurnRunsNothing() {
        BenchmarkHandle handle = backend.submit(produceAt(System.currentTimeMillis() + 60_000));

        backend.stop(handle);
        long stoppedAt = System.nanoTime();
        BenchmarkStatus status = awaitTerminal(handle);

        assertEquals(TaskStatus.DONE, status.getState(), "stopped as asked, not failed");
        assertEquals(0, status.getRecordsProcessed());
        assertEquals(0, producersMade.get());
        assertTrue(
                System.nanoTime() - stoppedAt < TimeUnit.SECONDS.toNanos(10),
                "it ended on the stop, not at its start time");
    }

    @Test
    void aTaskWithoutAStartTimeStartsAtOnce() {
        BenchmarkStatus status = awaitTerminal(backend.submit(produceAt(0)));

        assertEquals(TaskStatus.DONE, status.getState(), status.getError());
        assertEquals(10, status.getRecordsProcessed());
        assertEquals(1, producersMade.get());
    }
}
