package com.klster.kates.engine;

import com.klster.kates.domain.TestResult.TaskStatus;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.inject.Named;
import org.apache.kafka.clients.consumer.ConsumerConfig;
import org.apache.kafka.clients.consumer.ConsumerRecords;
import org.apache.kafka.clients.consumer.KafkaConsumer;
import org.apache.kafka.clients.producer.KafkaProducer;
import org.apache.kafka.clients.producer.ProducerConfig;
import org.apache.kafka.clients.producer.ProducerRecord;
import org.apache.kafka.common.serialization.ByteArrayDeserializer;
import org.apache.kafka.common.serialization.ByteArraySerializer;
import org.eclipse.microprofile.config.inject.ConfigProperty;

import java.time.Duration;
import java.util.Collections;
import java.util.Map;
import java.util.Properties;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicLong;
import java.util.logging.Level;
import java.util.logging.Logger;

/**
 * In-process benchmark backend using Kafka client API and virtual threads.
 * No external coordinator required — Kates runs the workloads itself.
 */
@ApplicationScoped
@Named("native")
public class NativeKafkaBackend implements BenchmarkBackend {

    private static final Logger LOG = Logger.getLogger(NativeKafkaBackend.class.getName());

    private final String bootstrapServers;
    private final Map<String, WorkerState> activeWorkers = new ConcurrentHashMap<>();

    @Inject
    public NativeKafkaBackend(
            @ConfigProperty(name = "kates.kafka.bootstrap-servers") String bootstrapServers) {
        this.bootstrapServers = bootstrapServers;
    }

    @Override
    public String name() {
        return "native";
    }

    @Override
    public BenchmarkHandle submit(BenchmarkTask task) {
        WorkerState state = new WorkerState(task);
        activeWorkers.put(task.getTaskId(), state);

        Thread worker = Thread.ofVirtual()
                .name("kates-" + task.getTaskId())
                .start(() -> executeTask(task, state));

        state.thread = worker;
        LOG.info("Started native benchmark: " + task.getTaskId());
        return new BenchmarkHandle(name(), task.getTaskId(), state);
    }

    @Override
    public BenchmarkStatus poll(BenchmarkHandle handle) {
        WorkerState state = activeWorkers.get(handle.taskId());
        if (state == null) {
            return BenchmarkStatus.builder(TaskStatus.DONE).build();
        }
        return state.toStatus();
    }

    @Override
    public void stop(BenchmarkHandle handle) {
        WorkerState state = activeWorkers.get(handle.taskId());
        if (state != null) {
            state.stopRequested.set(true);
        }
    }

    private void executeTask(BenchmarkTask task, WorkerState state) {
        state.status = TaskStatus.RUNNING;
        state.startTimeMs = System.currentTimeMillis();

        try {
            switch (task.getWorkloadType()) {
                case PRODUCE -> runProducer(task, state);
                case CONSUME -> runConsumer(task, state);
                case ROUND_TRIP -> runProducer(task, state);
            }
            state.status = TaskStatus.DONE;
        } catch (Exception e) {
            LOG.log(Level.WARNING, "Native benchmark failed: " + task.getTaskId(), e);
            state.error = e.getMessage();
            state.status = TaskStatus.FAILED;
        } finally {
            state.endTimeMs = System.currentTimeMillis();
        }
    }

    private void runProducer(BenchmarkTask task, WorkerState state) {
        Properties props = new Properties();
        props.put(ProducerConfig.BOOTSTRAP_SERVERS_CONFIG, bootstrapServers);
        props.put(ProducerConfig.KEY_SERIALIZER_CLASS_CONFIG, ByteArraySerializer.class.getName());
        props.put(ProducerConfig.VALUE_SERIALIZER_CLASS_CONFIG, ByteArraySerializer.class.getName());
        task.getProducerConfig().forEach(props::put);

        long deadline = System.currentTimeMillis() + task.getDurationMs();
        long targetNanosPerMsg = task.getTargetMessagesPerSec() > 0
                ? 1_000_000_000L / task.getTargetMessagesPerSec()
                : 0;

        try (KafkaProducer<byte[], byte[]> producer = new KafkaProducer<>(props)) {
            long sent = 0;
            long nextSendNanos = System.nanoTime();

            while (!state.stopRequested.get()
                    && sent < task.getMaxMessages()
                    && System.currentTimeMillis() < deadline) {

                if (targetNanosPerMsg > 0) {
                    long now = System.nanoTime();
                    if (now < nextSendNanos) {
                        Thread.onSpinWait();
                        continue;
                    }
                    nextSendNanos = now + targetNanosPerMsg;
                }

                long seq = sent;
                long tsNanos = System.nanoTime();
                byte[] payload = SequencedPayload.encode(
                        seq, tsNanos, state.runIdHash, task.getRecordSize());
                state.ackTracker.recordSent(seq, tsNanos);

                long sendStart = System.nanoTime();
                producer.send(
                        new ProducerRecord<>(task.getTopic(), payload),
                        (metadata, exception) -> {
                            if (exception != null) {
                                state.errors.incrementAndGet();
                                state.ackTracker.recordFailed(seq);
                            } else {
                                state.ackTracker.recordAcked(seq);
                            }
                        });
                long latencyNs = System.nanoTime() - sendStart;
                double latencyMs = latencyNs / 1_000_000.0;

                state.recordsProcessed.incrementAndGet();
                state.histogram.recordLatency(latencyMs);
                sent++;
            }

            producer.flush();
        } catch (Exception e) {
            throw new BenchmarkException("Producer failed: " + e.getMessage(), e);
        }
    }

    private void runConsumer(BenchmarkTask task, WorkerState state) {
        Properties props = new Properties();
        props.put(ConsumerConfig.BOOTSTRAP_SERVERS_CONFIG, bootstrapServers);
        props.put(ConsumerConfig.GROUP_ID_CONFIG, task.getConsumerGroup());
        props.put(ConsumerConfig.KEY_DESERIALIZER_CLASS_CONFIG, ByteArrayDeserializer.class.getName());
        props.put(ConsumerConfig.VALUE_DESERIALIZER_CLASS_CONFIG, ByteArrayDeserializer.class.getName());
        props.put(ConsumerConfig.AUTO_OFFSET_RESET_CONFIG, "earliest");
        task.getConsumerConfig().forEach(props::put);

        long deadline = System.currentTimeMillis() + task.getDurationMs();

        try (KafkaConsumer<byte[], byte[]> consumer = new KafkaConsumer<>(props)) {
            consumer.subscribe(Collections.singletonList(task.getTopic()));

            long consumed = 0;
            while (!state.stopRequested.get()
                    && consumed < task.getMaxMessages()
                    && System.currentTimeMillis() < deadline) {

                ConsumerRecords<byte[], byte[]> records = consumer.poll(Duration.ofMillis(500));
                consumed += records.count();
                state.recordsProcessed.addAndGet(records.count());
            }
        } catch (Exception e) {
            throw new BenchmarkException("Consumer failed: " + e.getMessage(), e);
        }
    }

    static class WorkerState {
        final BenchmarkTask task;
        final AtomicLong recordsProcessed = new AtomicLong();
        final AtomicLong errors = new AtomicLong();
        final AtomicBoolean stopRequested = new AtomicBoolean();
        final LatencyHistogram histogram = new LatencyHistogram();
        final AckTracker ackTracker = new AckTracker();
        final long runIdHash;

        volatile TaskStatus status = TaskStatus.PENDING;
        volatile long startTimeMs;
        volatile long endTimeMs;
        volatile String error;
        volatile Thread thread;

        WorkerState(BenchmarkTask task) {
            this.task = task;
            this.runIdHash = SequencedPayload.hashRunId(task.getTaskId());
        }

        BenchmarkStatus toStatus() {
            long records = recordsProcessed.get();
            long elapsed = (endTimeMs > 0 ? endTimeMs : System.currentTimeMillis()) - startTimeMs;
            double elapsedSec = Math.max(0.001, elapsed / 1000.0);
            double throughput = records / elapsedSec;

            BenchmarkStatus.Builder builder = BenchmarkStatus.builder(status)
                    .recordsProcessed(records)
                    .throughputRecordsPerSec(throughput)
                    .throughputMBPerSec(throughput * task.getRecordSize() / (1024.0 * 1024.0))
                    .avgLatencyMs(histogram.getMean())
                    .p50LatencyMs(histogram.getPercentile(50))
                    .p95LatencyMs(histogram.getPercentile(95))
                    .p99LatencyMs(histogram.getPercentile(99))
                    .p999LatencyMs(histogram.getPercentile(99.9))
                    .maxLatencyMs(histogram.getMax())
                    .latencyHistogram(histogram.snapshot());

            if (error != null) {
                builder.error(error);
            }
            return builder.build();
        }
    }
}
