package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Properties;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.LongFunction;

import org.apache.kafka.clients.consumer.ConsumerRecord;
import org.apache.kafka.clients.consumer.MockConsumer;
import org.apache.kafka.clients.consumer.OffsetResetStrategy;
import org.apache.kafka.clients.producer.Callback;
import org.apache.kafka.clients.producer.MockProducer;
import org.apache.kafka.clients.producer.ProducerRecord;
import org.apache.kafka.clients.producer.RecordMetadata;
import org.apache.kafka.common.PartitionInfo;
import org.apache.kafka.common.TopicPartition;
import org.apache.kafka.common.serialization.ByteArraySerializer;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.config.KafkaSecurityConfig;
import com.bmscomp.kates.domain.TestResult.TaskStatus;

/**
 * What a native ROUND_TRIP task measures. It ran the producer alone, so the
 * latency it reported as end to end was each send's time to acknowledgement,
 * and no record was ever read back.
 *
 * <p>The broker here is a pair of mock clients: every send is acknowledged at
 * once and reaches the consumer a fixed delay later, so an end-to-end sample is
 * at least that delay and an acknowledgement sample is close to zero.
 */
class NativeKafkaBackendRoundTripTest {

    private static final String TOPIC = "round_trip-test";
    private static final TopicPartition P0 = new TopicPartition(TOPIC, 0);
    private static final long DELIVERY_DELAY_MS = 50;

    /** Unique per task: the latency gauges are tagged with the task id. */
    private static final AtomicInteger SEQ = new AtomicInteger();

    private final ScheduledExecutorService delivery = Executors.newSingleThreadScheduledExecutor();

    @AfterEach
    void stopDelivery() {
        delivery.shutdownNow();
    }

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

    private static BenchmarkTask roundTrip(int records, Map<String, String> consumerConfig) {
        return BenchmarkTask.builder("rt-" + SEQ.incrementAndGet(), BenchmarkTask.WorkloadType.ROUND_TRIP)
                .runId("run-rt")
                .topic(TOPIC)
                .partitions(1)
                .maxMessages(records)
                .durationMs(30_000)
                .recordSize(64)
                .consumerConfig(consumerConfig)
                .build();
    }

    /** One partition, empty at {@code endOffset}. */
    private static MockConsumer<byte[], byte[]> consumerAt(long endOffset) {
        // EARLIEST, so that a consumer left to its reset policy would read
        // from the start; only an explicit seek to the end skips what is there.
        MockConsumer<byte[], byte[]> consumer = new MockConsumer<>(OffsetResetStrategy.EARLIEST);
        consumer.updatePartitions(TOPIC, List.of(new PartitionInfo(TOPIC, 0, null, null, null)));
        consumer.updateBeginningOffsets(Map.of(P0, 0L));
        consumer.updateEndOffsets(Map.of(P0, endOffset));
        return consumer;
    }

    /**
     * Acknowledges every send at once and hands the record to the consumer
     * {@link #DELIVERY_DELAY_MS} later, from {@code firstOffset} on. Before the
     * first record, it delivers whatever {@code before} makes of that offset.
     */
    private MockProducer<byte[], byte[]> producerFeeding(
            MockConsumer<byte[], byte[]> consumer, long firstOffset, LongFunction<List<byte[]>> before) {
        AtomicLong nextOffset = new AtomicLong(firstOffset);
        return new MockProducer<>(true, new ByteArraySerializer(), new ByteArraySerializer()) {
            @Override
            public synchronized Future<RecordMetadata> send(ProducerRecord<byte[], byte[]> record, Callback callback) {
                Future<RecordMetadata> acked = super.send(record, callback);
                if (nextOffset.get() == firstOffset) {
                    for (byte[] value : before.apply(firstOffset)) {
                        consumer.addRecord(new ConsumerRecord<>(TOPIC, 0, nextOffset.getAndIncrement(), null, value));
                    }
                }
                long offset = nextOffset.getAndIncrement();
                delivery.schedule(
                        () -> consumer.addRecord(new ConsumerRecord<>(TOPIC, 0, offset, null, record.value())),
                        DELIVERY_DELAY_MS,
                        TimeUnit.MILLISECONDS);
                return acked;
            }
        };
    }

    private static BenchmarkStatus runToEnd(
            BenchmarkTask task, MockProducer<byte[], byte[]> producer, MockConsumer<byte[], byte[]> consumer) {
        return runToEnd(task, producer, props -> consumer);
    }

    private static BenchmarkStatus runToEnd(
            BenchmarkTask task,
            MockProducer<byte[], byte[]> producer,
            java.util.function.Function<Properties, org.apache.kafka.clients.consumer.Consumer<byte[], byte[]>>
                    consumers) {
        NativeKafkaBackend backend =
                new NativeKafkaBackend("localhost:9092", plaintext(), null, props -> producer, consumers);
        BenchmarkHandle handle = backend.submit(task);
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(20);
        BenchmarkStatus status = backend.poll(handle);
        while (!status.isTerminal()) {
            assertTrue(System.nanoTime() < deadline, "the round trip did not finish: " + status.getState());
            java.util.concurrent.locks.LockSupport.parkNanos(TimeUnit.MILLISECONDS.toNanos(20));
            status = backend.poll(handle);
        }
        return status;
    }

    private static long samples(BenchmarkStatus status) {
        return Arrays.stream(status.getHeatmapBuckets()).sum();
    }

    @Test
    void latencyRunsFromTheSendToTheConsumersReceipt() {
        MockConsumer<byte[], byte[]> consumer = consumerAt(0);
        MockProducer<byte[], byte[]> producer = producerFeeding(consumer, 0, offset -> List.of());

        BenchmarkStatus status = runToEnd(roundTrip(200, Map.of()), producer, consumer);

        assertEquals(TaskStatus.DONE, status.getState(), status.getError());
        assertNull(status.getError());
        assertEquals(200, producer.history().size());
        assertEquals(200, status.getRecordsProcessed(), "the records that came back");
        // Acknowledgement is immediate here; had its samples gone into the
        // histogram there would be 400 of them and the median would be ~0.
        assertEquals(200, samples(status), "one sample per record received, none per acknowledgement");
        assertTrue(
                status.getP50LatencyMs() >= DELIVERY_DELAY_MS,
                "P50 " + status.getP50LatencyMs() + " ms is below the " + DELIVERY_DELAY_MS + " ms delivery delay");
    }

    @Test
    void onlyThisTasksRecordsAreTimed() {
        MockConsumer<byte[], byte[]> consumer = consumerAt(0);
        long runIdHash = SequencedPayload.hashRunId("run-rt");
        MockProducer<byte[], byte[]> producer = producerFeeding(
                consumer,
                0,
                offset -> List.of(
                        // Another run's record.
                        SequencedPayload.encode(0, System.nanoTime(), SequencedPayload.hashRunId("other-run"), 64),
                        // This run's id, but stamped an hour before the task started:
                        // a stamp from another clock, or another attempt.
                        SequencedPayload.encode(0, System.nanoTime() - TimeUnit.HOURS.toNanos(1), runIdHash, 64),
                        // Not a Kates payload at all.
                        new byte[] {1, 2, 3}));

        BenchmarkStatus status = runToEnd(roundTrip(50, Map.of()), producer, consumer);

        assertEquals(TaskStatus.DONE, status.getState(), status.getError());
        assertEquals(50, status.getRecordsProcessed());
        assertEquals(50, samples(status));
        assertTrue(status.getMaxLatencyMs() < 10_000, "max " + status.getMaxLatencyMs() + " ms");
    }

    @Test
    void recordsAlreadyOnTheTopicAreNotRead() {
        // Five records of this very run sit on the topic before the task
        // starts. A consumer left at its reset policy (EARLIEST here) would read
        // and count them; one positioned at the end never sees them.
        MockConsumer<byte[], byte[]> consumer = consumerAt(5);
        long runIdHash = SequencedPayload.hashRunId("run-rt");
        MockProducer<byte[], byte[]> producer = producerFeeding(consumer, 0, offset -> {
            List<byte[]> backlog = new ArrayList<>();
            for (int i = 0; i < 5; i++) {
                backlog.add(SequencedPayload.encode(i, System.nanoTime(), runIdHash, 64));
            }
            return backlog;
        });

        BenchmarkStatus status = runToEnd(roundTrip(20, Map.of()), producer, consumer);

        assertEquals(TaskStatus.DONE, status.getState(), status.getError());
        assertEquals(20, status.getRecordsProcessed());
    }

    @Test
    void theConsumerReadsWithoutAGroupAndTakesTheTasksConsumerConfig() {
        MockConsumer<byte[], byte[]> consumer = consumerAt(0);
        MockProducer<byte[], byte[]> producer = producerFeeding(consumer, 0, offset -> List.of());
        AtomicReference<Properties> seen = new AtomicReference<>();

        BenchmarkStatus status =
                runToEnd(roundTrip(10, Map.of("isolation.level", "read_committed")), producer, props -> {
                    seen.set(props);
                    return consumer;
                });

        assertEquals(TaskStatus.DONE, status.getState(), status.getError());
        Properties props = seen.get();
        assertEquals("read_committed", props.get("isolation.level"));
        assertFalse(props.containsKey("group.id"), "assigned partitions need no group: " + props);
        assertEquals("false", props.get("enable.auto.commit"));
    }

    @Test
    void aProducerThatFailsFailsTheTask() {
        MockConsumer<byte[], byte[]> consumer = consumerAt(0);
        MockProducer<byte[], byte[]> producer = producerFeeding(consumer, 0, offset -> List.of());
        producer.sendException = new org.apache.kafka.common.errors.AuthorizationException("not allowed");

        BenchmarkStatus status = runToEnd(roundTrip(10, Map.of()), producer, consumer);

        assertEquals(TaskStatus.FAILED, status.getState());
        assertTrue(status.getError().contains("not allowed"), status.getError());
    }

    @Test
    void aStampIsTimedOnlyInsideTheTasksWindow() {
        long hash = SequencedPayload.hashRunId("run-rt");
        long start = 1_000_000L;
        long received = 5_000_000L;

        assertEquals(
                3_000_000L,
                NativeKafkaBackend.endToEndNanos(
                        SequencedPayload.encode(7, 2_000_000L, hash, 64), hash, start, received));
        assertEquals(
                -1,
                NativeKafkaBackend.endToEndNanos(
                        SequencedPayload.encode(7, start - 1, hash, 64), hash, start, received),
                "stamped before the task started");
        assertEquals(
                -1,
                NativeKafkaBackend.endToEndNanos(
                        SequencedPayload.encode(7, received + 1, hash, 64), hash, start, received),
                "stamped after it arrived");
        assertEquals(
                -1,
                NativeKafkaBackend.endToEndNanos(
                        SequencedPayload.encode(7, 2_000_000L, hash + 1, 64), hash, start, received),
                "another run's");
        assertEquals(-1, NativeKafkaBackend.endToEndNanos(new byte[4], hash, start, received));
        assertEquals(-1, NativeKafkaBackend.endToEndNanos(null, hash, start, received));
    }

    @Test
    void theWindowHoldsAcrossTheClocksWrap() {
        // nanoTime may be any long, and differences stay right where
        // comparisons break.
        long hash = 42;
        long start = Long.MAX_VALUE - 10;
        long sent = Long.MAX_VALUE - 5;
        long received = Long.MIN_VALUE + 4;

        assertEquals(
                10,
                NativeKafkaBackend.endToEndNanos(SequencedPayload.encode(0, sent, hash, 64), hash, start, received));
    }
}
