package com.bmscomp.kates.engine;

import java.time.Duration;
import java.util.Collections;
import java.util.Map;
import java.util.Properties;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicLong;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.inject.Named;

import org.apache.kafka.clients.consumer.ConsumerConfig;
import org.apache.kafka.clients.consumer.ConsumerRecord;
import org.apache.kafka.clients.consumer.ConsumerRecords;
import org.apache.kafka.clients.consumer.KafkaConsumer;
import org.apache.kafka.clients.producer.KafkaProducer;
import org.apache.kafka.clients.producer.ProducerConfig;
import org.apache.kafka.clients.producer.ProducerRecord;
import org.apache.kafka.common.serialization.ByteArrayDeserializer;
import org.apache.kafka.common.serialization.ByteArraySerializer;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import com.bmscomp.kates.config.KafkaSecurityConfig;
import com.bmscomp.kates.domain.IntegrityResult;
import com.bmscomp.kates.domain.TestResult.TaskStatus;

/**
 * In-process benchmark backend using Kafka client API and virtual threads.
 * No external coordinator required — Kates runs the workloads itself.
 */
@ApplicationScoped
@Named("native")
public class NativeKafkaBackend implements BenchmarkBackend {

    private static final Logger LOG = Logger.getLogger(NativeKafkaBackend.class);

    private final String bootstrapServers;
    private final KafkaSecurityConfig securityConfig;
    private final Map<String, WorkerState> activeWorkers = new ConcurrentHashMap<>();

    @Inject
    public NativeKafkaBackend(
            @ConfigProperty(name = "kates.kafka.bootstrap-servers") String bootstrapServers,
            KafkaSecurityConfig securityConfig) {
        this.bootstrapServers = bootstrapServers;
        this.securityConfig = securityConfig;
    }

    @Override
    public String name() {
        return "native";
    }

    @Override
    public BenchmarkHandle submit(BenchmarkTask task) {
        WorkerState state = new WorkerState(task);
        activeWorkers.put(task.getTaskId(), state);

        Thread worker = Thread.ofVirtual().name("kates-" + task.getTaskId()).start(() -> executeTask(task, state));

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
                case INTEGRITY -> runIntegrity(task, state);
            }
            state.status = TaskStatus.DONE;
        } catch (Exception e) {
            LOG.warn("Native benchmark failed: " + task.getTaskId(), e);
            state.error = e.getMessage();
            state.status = TaskStatus.FAILED;
        } finally {
            state.endTimeMs = System.currentTimeMillis();
        }
    }

    private void runIntegrity(BenchmarkTask task, WorkerState state) {
        LOG.info("Integrity: starting produce phase for " + task.getTaskId());
        runProducer(task, state);

        LOG.info("Integrity: produce complete (" + state.recordsProcessed.get()
                + " records). Starting consume phase...");

        state.verifier = new DataIntegrityVerifier(state.ackTracker);
        runIntegrityConsumer(task, state);

        LOG.info("Integrity: consume complete. Running reconciliation...");

        state.integrityResult = state.verifier.verify(
                -1, task.isEnableCrc(), true, task.isEnableIdempotence(), task.isEnableTransactions());
    }

    private void runProducer(BenchmarkTask task, WorkerState state) {
        Properties props = new Properties();
        props.put(ProducerConfig.BOOTSTRAP_SERVERS_CONFIG, bootstrapServers);
        props.put(ProducerConfig.KEY_SERIALIZER_CLASS_CONFIG, ByteArraySerializer.class.getName());
        props.put(ProducerConfig.VALUE_SERIALIZER_CLASS_CONFIG, ByteArraySerializer.class.getName());

        if (task.isEnableIdempotence()) {
            props.put(ProducerConfig.ENABLE_IDEMPOTENCE_CONFIG, "true");
            props.put(ProducerConfig.MAX_IN_FLIGHT_REQUESTS_PER_CONNECTION, "5");
        }

        if (task.isEnableTransactions()) {
            props.put(ProducerConfig.TRANSACTIONAL_ID_CONFIG, "kates-" + task.getTaskId());
            props.put(ProducerConfig.ENABLE_IDEMPOTENCE_CONFIG, "true");
        }

        props.put(ProducerConfig.METRIC_REPORTER_CLASSES_CONFIG, "");
        securityConfig.apply(props);
        task.getProducerConfig().forEach(props::put);

        long deadline = System.currentTimeMillis() + task.getDurationMs();
        long targetNanosPerMsg =
                task.getTargetMessagesPerSec() > 0 ? 1_000_000_000L / task.getTargetMessagesPerSec() : 0;

        try (KafkaProducer<byte[], byte[]> producer = new KafkaProducer<>(props)) {
            if (task.isEnableTransactions()) {
                producer.initTransactions();
            }

            long sent = 0;
            long nextSendNanos = System.nanoTime();
            int txBatchSize = 100;

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

                if (task.isEnableTransactions() && sent % txBatchSize == 0) {
                    if (sent > 0) producer.commitTransaction();
                    producer.beginTransaction();
                }

                long seq = sent;
                long tsNanos = System.nanoTime();
                byte[] payload = SequencedPayload.encode(seq, tsNanos, state.runIdHash, task.getRecordSize());
                state.ackTracker.recordSent(seq, tsNanos);

                long sendStart = System.nanoTime();
                producer.send(new ProducerRecord<>(task.getTopic(), payload), (metadata, exception) -> {
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

            if (task.isEnableTransactions()) {
                producer.commitTransaction();
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
        props.put(ConsumerConfig.METRIC_REPORTER_CLASSES_CONFIG, "");
        securityConfig.apply(props);
        task.getConsumerConfig().forEach(props::put);

        long deadline = System.currentTimeMillis() + task.getDurationMs();
        int emptyPollStreak = 0;
        int maxEmptyPolls = 20;

        try (KafkaConsumer<byte[], byte[]> consumer = new KafkaConsumer<>(props)) {
            consumer.subscribe(Collections.singletonList(task.getTopic()));

            long consumed = 0;
            while (!state.stopRequested.get()
                    && consumed < task.getMaxMessages()
                    && System.currentTimeMillis() < deadline) {

                ConsumerRecords<byte[], byte[]> records = consumer.poll(Duration.ofMillis(500));
                if (records.isEmpty()) {
                    emptyPollStreak++;
                    if (emptyPollStreak >= maxEmptyPolls && consumed > 0) {
                        break;
                    }
                    continue;
                }
                emptyPollStreak = 0;

                for (ConsumerRecord<byte[], byte[]> record : records) {
                    try {
                        SequencedPayload payload = SequencedPayload.decode(record.value());
                        if (payload.getRunIdHash() != state.runIdHash) {
                            continue;
                        }
                        consumed++;
                        state.recordsProcessed.incrementAndGet();
                    } catch (Exception e) {
                        LOG.debug("Skipping malformed record at offset " + record.offset());
                    }
                }
            }
        } catch (Exception e) {
            throw new BenchmarkException("Consumer failed: " + e.getMessage(), e);
        }
    }

    private void runIntegrityConsumer(BenchmarkTask task, WorkerState state) {
        Properties props = new Properties();
        props.put(ConsumerConfig.BOOTSTRAP_SERVERS_CONFIG, bootstrapServers);
        props.put(ConsumerConfig.GROUP_ID_CONFIG, task.getConsumerGroup() + "-integrity");
        props.put(ConsumerConfig.KEY_DESERIALIZER_CLASS_CONFIG, ByteArrayDeserializer.class.getName());
        props.put(ConsumerConfig.VALUE_DESERIALIZER_CLASS_CONFIG, ByteArrayDeserializer.class.getName());
        props.put(ConsumerConfig.AUTO_OFFSET_RESET_CONFIG, "earliest");

        if (task.isEnableTransactions()) {
            props.put(ConsumerConfig.ISOLATION_LEVEL_CONFIG, "read_committed");
        }

        props.put(ConsumerConfig.METRIC_REPORTER_CLASSES_CONFIG, "");
        securityConfig.apply(props);
        task.getConsumerConfig().forEach(props::put);

        long deadline = System.currentTimeMillis() + task.getDurationMs();
        long expectedRecords = state.recordsProcessed.get();
        int emptyPollStreak = 0;
        int maxEmptyPolls = 20;

        try (KafkaConsumer<byte[], byte[]> consumer = new KafkaConsumer<>(props)) {
            consumer.subscribe(Collections.singletonList(task.getTopic()));

            long consumed = 0;
            while (!state.stopRequested.get() && System.currentTimeMillis() < deadline) {

                ConsumerRecords<byte[], byte[]> records = consumer.poll(Duration.ofMillis(1000));
                if (records.isEmpty()) {
                    emptyPollStreak++;
                    if (emptyPollStreak >= maxEmptyPolls && consumed >= expectedRecords) {
                        break;
                    }
                    continue;
                }
                emptyPollStreak = 0;

                for (ConsumerRecord<byte[], byte[]> record : records) {
                    try {
                        SequencedPayload payload = SequencedPayload.decode(record.value());

                        if (payload.getRunIdHash() != state.runIdHash) {
                            continue;
                        }

                        boolean crcOk = !task.isEnableCrc() || payload.isCrcValid();
                        state.verifier.recordConsumed(payload.getSequence(), crcOk, record.partition());
                        consumed++;
                    } catch (Exception e) {
                        LOG.debug("Skipping malformed record at offset " + record.offset());
                    }
                }
            }

            LOG.info("Integrity consumer: consumed " + consumed + " records (expected " + expectedRecords + ")");
        } catch (Exception e) {
            throw new BenchmarkException("Integrity consumer failed: " + e.getMessage(), e);
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
        volatile DataIntegrityVerifier verifier;
        volatile IntegrityResult integrityResult;

        WorkerState(BenchmarkTask task) {
            this.task = task;
            String hashSource = task.getRunId() != null ? task.getRunId() : task.getTaskId();
            this.runIdHash = SequencedPayload.hashRunId(hashSource);
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
                    .latencyHistogram(histogram.snapshot())
                    .heatmapBuckets(histogram.exportBuckets());

            if (error != null) {
                builder.error(error);
            }

            if (integrityResult != null) {
                builder.integrityResult(integrityResult);
            }

            return builder.build();
        }
    }
}
