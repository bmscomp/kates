package com.bmscomp.kates.engine;

import java.time.Duration;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.Properties;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;
import java.util.concurrent.locks.LockSupport;
import java.util.function.Function;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.inject.Named;

import org.apache.kafka.clients.consumer.Consumer;
import org.apache.kafka.clients.consumer.ConsumerConfig;
import org.apache.kafka.clients.consumer.ConsumerRecord;
import org.apache.kafka.clients.consumer.ConsumerRecords;
import org.apache.kafka.clients.consumer.KafkaConsumer;
import org.apache.kafka.clients.producer.KafkaProducer;
import org.apache.kafka.clients.producer.Producer;
import org.apache.kafka.clients.producer.ProducerConfig;
import org.apache.kafka.clients.producer.ProducerRecord;
import org.apache.kafka.common.PartitionInfo;
import org.apache.kafka.common.TopicPartition;
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
    private final CdcIntegrationService cdcIntegrationService;
    private final Map<String, WorkerState> activeWorkers = new ConcurrentHashMap<>();

    /**
     * Task ids that have finished, oldest first. Finished workers used to stay
     * in {@link #activeWorkers} for the life of the JVM, each still holding its
     * ack bitset and histogram; the orchestrator polls a task after it ends, so
     * they cannot be dropped immediately either. Instead a finished worker
     * releases its heavy state right away and its final status is retained for
     * the most recent {@link #MAX_RETAINED_COMPLETED} tasks.
     */
    private final java.util.Queue<String> completedOrder = new java.util.concurrent.ConcurrentLinkedQueue<>();

    private static final int MAX_RETAINED_COMPLETED = 500;

    /**
     * A transactional producer commits after this many records, or once its
     * transaction has been open {@link #TX_MAX_OPEN_NANOS}, whichever comes
     * first. By count alone a producer slower than about 1.7 records/s kept a
     * transaction open past the client's transaction.timeout.ms, 60 s by
     * default; the coordinator aborted it and the next commit failed the task.
     */
    static final int TX_BATCH_RECORDS = 100;

    /** A sixth of the client's default transaction.timeout.ms, which Kates does not change. */
    static final long TX_MAX_OPEN_NANOS = java.util.concurrent.TimeUnit.SECONDS.toNanos(10);

    /**
     * How long a ROUND_TRIP consumer keeps waiting, once the producer has
     * finished, with none of the missing records arriving. A CONSUME task
     * gives up after the same 10 s of empty polls.
     */
    static final long ROUND_TRIP_DRAIN_NANOS = TimeUnit.SECONDS.toNanos(10);

    /** How long a ROUND_TRIP consumer waits for the topic's partitions to be visible. */
    private static final long PARTITIONS_WAIT_NANOS = TimeUnit.SECONDS.toNanos(30);

    /** Makes the Kafka clients; a test hands in mock ones. */
    private final Function<Properties, Producer<byte[], byte[]>> producers;

    private final Function<Properties, Consumer<byte[], byte[]>> consumers;

    @Inject
    public NativeKafkaBackend(
            @ConfigProperty(name = "kates.kafka.bootstrap-servers") String bootstrapServers,
            KafkaSecurityConfig securityConfig,
            CdcIntegrationService cdcIntegrationService) {
        this(bootstrapServers, securityConfig, cdcIntegrationService, KafkaProducer::new, KafkaConsumer::new);
    }

    // Package-private: a round trip needs a broker at both ends, and a test
    // stands in for it through the clients.
    NativeKafkaBackend(
            String bootstrapServers,
            KafkaSecurityConfig securityConfig,
            CdcIntegrationService cdcIntegrationService,
            Function<Properties, Producer<byte[], byte[]>> producers,
            Function<Properties, Consumer<byte[], byte[]>> consumers) {
        this.bootstrapServers = bootstrapServers;
        this.securityConfig = securityConfig;
        this.cdcIntegrationService = cdcIntegrationService;
        this.producers = producers;
        this.consumers = consumers;
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
        WorkerState state = resolve(handle);
        if (state == null) {
            // Previously this returned DONE. An unknown task is not a finished
            // task: the only ways to get here are a handle from a process that
            // has since restarted, or one the caller invented. Reporting success
            // for either turns "we lost track of your run" into "your run
            // passed", with zero records to show for it.
            return BenchmarkStatus.builder(TaskStatus.FAILED)
                    .error("Task " + handle.taskId() + " is not known to this backend."
                            + " Its worker was lost, most likely because the process restarted.")
                    .build();
        }
        BenchmarkStatus finalStatus = state.finalStatus;
        return finalStatus != null ? finalStatus : state.toStatus();
    }

    @Override
    public void stop(BenchmarkHandle handle) {
        WorkerState state = resolve(handle);
        if (state != null) {
            state.stopRequested.set(true);
        }
    }

    /** The first fault wins: RPO is measured from the moment the cluster first broke. */
    @Override
    public void markChaosStart(BenchmarkHandle handle, long chaosStartNanos) {
        WorkerState state = resolve(handle);
        if (state != null && chaosStartNanos > 0) {
            state.chaosStartNanos.compareAndSet(-1, chaosStartNanos);
        }
    }

    /**
     * The worker behind a handle, from the live map or from the handle itself.
     *
     * <p>Looking only in the map made both {@code poll} and {@code stop} depend
     * on the retention limit: past {@link #MAX_RETAINED_COMPLETED} completed
     * tasks the entry is evicted, and a still-running task would then poll as if
     * it had never existed and ignore a stop request. The handle has held the
     * state all along.
     */
    private WorkerState resolve(BenchmarkHandle handle) {
        WorkerState state = activeWorkers.get(handle.taskId());
        if (state != null) {
            return state;
        }
        return handle.internalRef() instanceof WorkerState fromHandle ? fromHandle : null;
    }

    private void executeTask(BenchmarkTask task, WorkerState state) {
        state.status = TaskStatus.RUNNING;
        state.startTimeMs = System.currentTimeMillis();

        try {
            switch (task.getWorkloadType()) {
                case PRODUCE -> runProducer(task, state);
                case CONSUME -> runConsumer(task, state);
                case ROUND_TRIP -> runRoundTrip(task, state);
                case INTEGRITY -> runIntegrity(task, state);
                case INTEGRITY_CDC -> runIntegrityCdc(task, state);
            }
            if (state.status != TaskStatus.FAILED) {
                applyPostConditions(task, state);
            }
        } catch (Throwable t) {
            // Throwable, not Exception. The compression codecs surface a missing
            // implementation as an AssertionError, which is an Error — it slipped
            // past a catch of Exception, killed the virtual thread, and left the
            // snapshot taken below reading RUNNING. The task then polled as
            // running forever, until the timeout reaper eventually killed it,
            // half an hour after the thing had already died.
            LOG.warn("Native benchmark failed: " + task.getTaskId(), t);
            state.error = describe(t);
            state.status = TaskStatus.FAILED;
            if (t instanceof InterruptedException) {
                Thread.currentThread().interrupt();
            }
        } finally {
            state.endTimeMs = System.currentTimeMillis();
            // Snapshot the terminal status BEFORE releasing anything: every
            // later poll is served from this immutable copy, so percentiles and
            // record counts survive the cleanup below.
            state.finalStatus = state.toStatus();
            // Unregister the per-task latency gauges: task ids are unbounded, so
            // leaving them registered would grow the meter registry for the life
            // of the process. Only the METERS go away — the histogram data stays
            // readable, so the final poll still reports accurate percentiles.
            state.histogram.close();
            state.releaseHeavyState();
            retireWorker(task.getTaskId());
        }
    }

    /**
     * Decides DONE or FAILED for a workload that ran to completion without
     * throwing.
     *
     * <p>"The method returned" used to be the whole test, which made success the
     * default outcome for anything that failed quietly. A consumer whose
     * producer never produced polls an empty topic for its full duration and
     * returns normally: it was reported DONE, having received nothing. That is
     * how a run could show a failed produce task at 0 records/s next to a
     * consume task marked DONE, also at 0 records/s.
     *
     * <p>A producer had the mirror image of the problem. Send failures arrive on
     * a callback, where they were counted into {@code errors} and never read
     * again, so a producer whose every record was rejected by the broker also
     * returned normally and was also called DONE.
     */
    // Package-private: this is the whole DONE/FAILED decision, and reaching it
    // through a real run would need a broker.
    void applyPostConditions(BenchmarkTask task, WorkerState state) {
        state.status = TaskStatus.DONE;

        if (state.stopRequested.get()) {
            // Asked to stop early, so a short count is the instruction being
            // obeyed rather than a failure.
            return;
        }

        long processed = state.recordsProcessed.get();
        long failedSends = state.errors.get();

        switch (task.getWorkloadType()) {
            case CONSUME -> {
                if (processed == 0) {
                    state.status = TaskStatus.FAILED;
                    state.error = "Consumed 0 records from " + task.getTopic() + " in "
                            + task.getDurationMs() + "ms. The topic was empty for the whole run"
                            + " — check whether the producer in this run failed.";
                }
            }
            case ROUND_TRIP -> applyRoundTripPostConditions(task, state);
            case PRODUCE, INTEGRITY -> {
                // recordsProcessed counts sends handed to the client; errors
                // counts the ones the broker then rejected. Everything rejected
                // means nothing reached the topic.
                if (processed > 0 && failedSends >= processed) {
                    state.status = TaskStatus.FAILED;
                    state.error = "All " + processed + " sends were rejected by the broker"
                            + (state.firstSendError != null ? ": " + state.firstSendError : ".");
                } else if (failedSends > 0) {
                    // Partial loss is a benchmark result, not a broken run, but
                    // it must be visible rather than rounded away.
                    LOG.warnf(
                            "Task %s: %d of %d sends were rejected: %s",
                            task.getTaskId(), failedSends, processed, state.firstSendError);
                    state.error = failedSends + " of " + processed + " sends were rejected"
                            + (state.firstSendError != null ? ": " + state.firstSendError : ".");
                }
            }
            case INTEGRITY_CDC -> {
                // runIntegrityCdc sets its own status from the CDC service.
            }
        }
    }

    /**
     * A round trip has two sides to answer for. Its records are the ones that
     * came back, so a send the broker rejected and a record it acknowledged
     * that never reached the consumer are different shortfalls, each named.
     */
    private static void applyRoundTripPostConditions(BenchmarkTask task, WorkerState state) {
        long sent = state.recordsSent.get();
        long rejected = state.errors.get();
        long acknowledged = sent - rejected;
        long received = state.recordsProcessed.get();
        String reason = state.firstSendError != null ? ": " + state.firstSendError : "";

        if (sent > 0 && rejected >= sent) {
            state.status = TaskStatus.FAILED;
            state.error = "All " + sent + " sends were rejected by the broker" + (reason.isEmpty() ? "." : reason);
            return;
        }
        if (acknowledged > 0 && received == 0) {
            state.status = TaskStatus.FAILED;
            state.error = "None of the " + acknowledged + " records the broker acknowledged came back from "
                    + task.getTopic() + "; the consumer waited "
                    + TimeUnit.NANOSECONDS.toSeconds(ROUND_TRIP_DRAIN_NANOS) + " s after the last send.";
            return;
        }

        // Partial loss on either side is a result, not a broken run, but it
        // must be visible rather than rounded away.
        List<String> shortfalls = new ArrayList<>(2);
        if (rejected > 0) {
            shortfalls.add(rejected + " of " + sent + " sends were rejected" + reason);
        }
        if (received < acknowledged) {
            shortfalls.add((acknowledged - received) + " of " + acknowledged
                    + " acknowledged records did not come back, the consumer stopping after "
                    + TimeUnit.NANOSECONDS.toSeconds(ROUND_TRIP_DRAIN_NANOS) + " s with none arriving");
        }
        if (!shortfalls.isEmpty()) {
            state.error = String.join("; ", shortfalls);
            LOG.warnf("Task %s: %s", task.getTaskId(), state.error);
        }
    }

    /** A message for a failure, falling back to the type when there is none. */
    private static String describe(Throwable t) {
        String message = t.getMessage();
        return message != null && !message.isBlank() ? message : t.getClass().getName();
    }

    /**
     * Records a finished task and evicts the oldest retained ones, so the
     * worker map stays bounded no matter how many tasks the process runs.
     */
    private void retireWorker(String taskId) {
        completedOrder.add(taskId);
        while (completedOrder.size() > MAX_RETAINED_COMPLETED) {
            String evicted = completedOrder.poll();
            if (evicted == null) {
                break;
            }
            activeWorkers.remove(evicted);
        }
    }

    private void runIntegrityCdc(BenchmarkTask task, WorkerState state) {
        try {
            BenchmarkStatus cdcStatus = cdcIntegrationService
                    .runCdcTest(task, (phase, durations) -> {
                        state.currentPhase = phase;
                        state.cdcPhaseDurations = durations;
                    })
                    .join();
            state.status = cdcStatus.getState();
            if (cdcStatus.getError() != null) {
                state.error = cdcStatus.getError();
            }
            state.recordsProcessed.set(cdcStatus.getRecordsProcessed());
            if (cdcStatus.getPhaseDurations() != null
                    && !cdcStatus.getPhaseDurations().isEmpty()) {
                state.cdcPhaseDurations = cdcStatus.getPhaseDurations();
            }
        } catch (Exception e) {
            throw new BenchmarkException("CDC Integration test failed: " + e.getMessage(), e);
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

        // -1 unless a resilience run marked its fault while this task ran; RPO
        // is then reported as not measured rather than as a zero.
        state.integrityResult = state.verifier.verify(
                state.chaosStartNanos.get(),
                task.isEnableCrc(),
                true,
                task.isEnableIdempotence(),
                task.isEnableTransactions());
    }

    private void runProducer(BenchmarkTask task, WorkerState state) {
        runProducer(task, state, state.recordsProcessed, state.histogram);
    }

    /**
     * @param sendCount counts each record handed to the client
     * @param ackLatency takes each send's time to its acknowledgement, or
     *     null to record none
     */
    private void runProducer(BenchmarkTask task, WorkerState state, AtomicLong sendCount, LatencyHistogram ackLatency) {
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

        try (Producer<byte[], byte[]> producer = producers.apply(props)) {
            if (task.isEnableTransactions()) {
                producer.initTransactions();
            }

            long sent = 0;
            long nextSendNanos = System.nanoTime();
            boolean txOpen = false;
            long txSent = 0;
            long txOpenedNanos = 0;

            while (!state.stopRequested.get()
                    && sent < task.getMaxMessages()
                    && System.currentTimeMillis() < deadline) {

                if (targetNanosPerMsg > 0) {
                    long now = System.nanoTime();
                    long waitNanos = nextSendNanos - now;
                    if (waitNanos > 0) {
                        // Park rather than spin. Thread.onSpinWait() in a loop
                        // pins the carrier thread for the whole inter-send gap,
                        // so N rate-limited producers burned N cores doing
                        // nothing — with virtual threads that starves every
                        // other task on the same carriers. parkNanos yields the
                        // carrier and is accurate enough at these intervals.
                        LockSupport.parkNanos(waitNanos);
                        continue;
                    }
                    nextSendNanos = now + targetNanosPerMsg;
                }

                if (task.isEnableTransactions()) {
                    long now = System.nanoTime();
                    if (txOpen && transactionDue(txSent, txOpenedNanos, now)) {
                        producer.commitTransaction();
                        txOpen = false;
                    }
                    if (!txOpen) {
                        producer.beginTransaction();
                        txOpen = true;
                        txSent = 0;
                        txOpenedNanos = now;
                    }
                }

                long seq = sent;
                long tsNanos = System.nanoTime();
                byte[] payload = SequencedPayload.encode(seq, tsNanos, state.runIdHash, task.getRecordSize());
                state.ackTracker.recordSent(seq, tsNanos);

                // Produce latency is the broker round-trip, which is only known
                // when the send is acknowledged in the callback. Measuring it at
                // send() return would capture accumulator-buffer append time
                // (near-zero with batching/linger), not real latency. sendStart
                // is captured here and the sample is recorded in the callback.
                final long sendStart = System.nanoTime();
                producer.send(new ProducerRecord<>(task.getTopic(), payload), (metadata, exception) -> {
                    if (exception != null) {
                        state.errors.incrementAndGet();
                        state.ackTracker.recordFailed(seq);
                        // Keep the first one. A count alone says a send was
                        // rejected but not why, which is the difference between
                        // a diagnosis and a shrug.
                        state.recordSendError(exception);
                    } else {
                        if (ackLatency != null) {
                            ackLatency.recordLatency((System.nanoTime() - sendStart) / 1_000_000.0);
                        }
                        // Hand the send timestamp back rather than having the
                        // tracker keep one per sequence.
                        state.ackTracker.recordAcked(seq, tsNanos);
                    }
                });

                sendCount.incrementAndGet();
                sent++;
                txSent++;
            }

            // Only a transaction that was begun: a producer that sent nothing
            // has none, and committing then throws.
            if (txOpen) {
                producer.commitTransaction();
            }

            producer.flush();
        } catch (Exception e) {
            throw new BenchmarkException("Producer failed: " + e.getMessage(), e);
        }
    }

    /** Whether an open transaction is due for its commit before the next record. */
    static boolean transactionDue(long sentInTransaction, long openedNanos, long nowNanos) {
        return sentInTransaction >= TX_BATCH_RECORDS || nowNanos - openedNanos >= TX_MAX_OPEN_NANOS;
    }

    /**
     * A producer and a consumer on one topic, the consumer timing each of the
     * task's records from its send to its receipt.
     *
     * <p>The producer stamps each payload with {@link System#nanoTime()} just
     * before the send, and the consumer subtracts that stamp from its own
     * reading of the same clock when the record arrives. nanoTime's origin is
     * arbitrary and differs between JVMs, so the two readings compare only
     * because both clients run in this process. The producer's time to
     * acknowledgement stays out of the histogram: it is a different interval,
     * and mixed in it would report neither.
     *
     * <p>The task's record count is the records that came back, the same ones
     * its latency describes; the sends are counted in {@code recordsSent}, and
     * {@link #applyRoundTripPostConditions} reports any shortfall between them.
     */
    private void runRoundTrip(BenchmarkTask task, WorkerState state) throws InterruptedException {
        try (Consumer<byte[], byte[]> consumer = consumers.apply(roundTripConsumerProps(task))) {
            List<TopicPartition> partitions = partitionsOf(consumer, task.getTopic());
            consumer.assign(partitions);
            // At the end, and resolved now rather than on the first poll: every
            // record the producer sends from here on is read, and nothing an
            // earlier run left on the topic is read ahead of it, which would add
            // that backlog's read time to every sample.
            consumer.seekToEnd(partitions);
            partitions.forEach(consumer::position);

            long startNanos = System.nanoTime();
            AtomicReference<Throwable> producerFailure = new AtomicReference<>();
            Thread producer = Thread.ofVirtual()
                    .name("kates-" + task.getTaskId() + "-producer")
                    .start(() -> {
                        try {
                            runProducer(task, state, state.recordsSent, null);
                        } catch (Throwable t) {
                            producerFailure.set(t);
                        }
                    });

            try {
                receiveRoundTrip(consumer, state, startNanos, producer, producerFailure);
            } catch (RuntimeException | Error e) {
                // Nothing is left to time the producer's records, so it stops too.
                state.stopRequested.set(true);
                throw e;
            } finally {
                producer.join();
            }

            Throwable failure = producerFailure.get();
            if (failure instanceof RuntimeException e) {
                throw e;
            }
            if (failure instanceof Error e) {
                throw e;
            }
            if (failure != null) {
                throw new BenchmarkException("Producer failed: " + describe(failure), failure);
            }
        } catch (BenchmarkException e) {
            throw e;
        } catch (RuntimeException e) {
            // The producer's failures arrive as BenchmarkException, so this is
            // the consumer's.
            throw new BenchmarkException("Consumer failed: " + describe(e), e);
        }
    }

    /**
     * Reads until every acknowledged record has come back, or until
     * {@link #ROUND_TRIP_DRAIN_NANOS} pass after the producer finishes with
     * none of the rest arriving.
     */
    private static void receiveRoundTrip(
            Consumer<byte[], byte[]> consumer,
            WorkerState state,
            long startNanos,
            Thread producer,
            AtomicReference<Throwable> producerFailure) {
        long quietSinceNanos = -1;
        while (!state.stopRequested.get() && producerFailure.get() == null) {
            ConsumerRecords<byte[], byte[]> records = consumer.poll(Duration.ofMillis(100));
            // One reading for the whole batch: the records arrived together, and
            // a reading per record would add the time spent on those before it.
            long receivedNanos = System.nanoTime();
            boolean arrived = false;
            for (ConsumerRecord<byte[], byte[]> record : records) {
                long latencyNanos = endToEndNanos(record.value(), state.runIdHash, startNanos, receivedNanos);
                if (latencyNanos >= 0) {
                    state.histogram.recordLatency(latencyNanos / 1_000_000.0);
                    state.recordsProcessed.incrementAndGet();
                    arrived = true;
                }
            }

            if (producer.isAlive()) {
                continue;
            }
            // The producer has flushed, so each send is acknowledged or rejected.
            long acknowledged = state.recordsSent.get() - state.errors.get();
            if (state.recordsProcessed.get() >= acknowledged) {
                return;
            }
            if (arrived || quietSinceNanos < 0) {
                quietSinceNanos = receivedNanos;
            } else if (receivedNanos - quietSinceNanos >= ROUND_TRIP_DRAIN_NANOS) {
                return;
            }
        }
    }

    /**
     * How long one received record took from its send, in nanoseconds, or -1
     * when it is not one of this task's records: not a Kates payload, another
     * run's, or stamped outside {@code [startNanos, receivedNanos]}.
     *
     * <p>The last check keeps the arithmetic in one clock domain. The stamp is
     * a nanoTime reading, meaningful only against the JVM that took it; a
     * record stamped before the task started, or after it arrived, cannot have
     * come from this task's producer, and its difference would be noise.
     */
    static long endToEndNanos(byte[] value, long runIdHash, long startNanos, long receivedNanos) {
        if (value == null || value.length < SequencedPayload.HEADER_SIZE) {
            return -1;
        }
        SequencedPayload payload = SequencedPayload.decode(value);
        if (payload.getRunIdHash() != runIdHash) {
            return -1;
        }
        long sentNanos = payload.getTimestampNanos();
        // Differences, not comparisons: nanoTime values may wrap.
        if (sentNanos - startNanos < 0 || receivedNanos - sentNanos < 0) {
            return -1;
        }
        return receivedNanos - sentNanos;
    }

    /**
     * Assigned partitions and no group: there is no rebalance to wait out
     * before the first record, and no offsets are left behind in a group.
     */
    private Properties roundTripConsumerProps(BenchmarkTask task) {
        Properties props = new Properties();
        props.put(ConsumerConfig.BOOTSTRAP_SERVERS_CONFIG, bootstrapServers);
        props.put(ConsumerConfig.KEY_DESERIALIZER_CLASS_CONFIG, ByteArrayDeserializer.class.getName());
        props.put(ConsumerConfig.VALUE_DESERIALIZER_CLASS_CONFIG, ByteArrayDeserializer.class.getName());
        props.put(ConsumerConfig.ENABLE_AUTO_COMMIT_CONFIG, "false");
        props.put(ConsumerConfig.METRIC_REPORTER_CLASSES_CONFIG, "");
        securityConfig.apply(props);
        task.getConsumerConfig().forEach(props::put);
        return props;
    }

    /**
     * The topic's partitions. The orchestrator creates the topic just before
     * the task starts, and a broker can answer a metadata request before it
     * has heard of the topic, so an empty answer is retried for a while.
     */
    private static List<TopicPartition> partitionsOf(Consumer<byte[], byte[]> consumer, String topic) {
        long deadline = System.nanoTime() + PARTITIONS_WAIT_NANOS;
        while (true) {
            List<PartitionInfo> infos = consumer.partitionsFor(topic);
            if (infos != null && !infos.isEmpty()) {
                return infos.stream()
                        .map(info -> new TopicPartition(info.topic(), info.partition()))
                        .toList();
            }
            if (System.nanoTime() - deadline >= 0) {
                throw new BenchmarkException("Topic " + topic + " has no partitions the consumer can see after "
                        + TimeUnit.NANOSECONDS.toSeconds(PARTITIONS_WAIT_NANOS) + " s.");
            }
            LockSupport.parkNanos(TimeUnit.MILLISECONDS.toNanos(200));
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

        try (Consumer<byte[], byte[]> consumer = consumers.apply(props)) {
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

        try (Consumer<byte[], byte[]> consumer = consumers.apply(props)) {
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
        /**
         * Records a ROUND_TRIP task's producer handed to the client. The task's
         * {@link #recordsProcessed} counts the records that came back instead,
         * the ones its latency describes.
         */
        final AtomicLong recordsSent = new AtomicLong();

        final AtomicLong errors = new AtomicLong();
        final AtomicBoolean stopRequested = new AtomicBoolean();
        /** {@link System#nanoTime()} of the first fault injected during the run, or -1. */
        final AtomicLong chaosStartNanos = new AtomicLong(-1);
        /**
         * Scoped to the TASK, not "global". Micrometer dedups gauges on
         * name+tags, so the old shared id meant only the very first worker in
         * the JVM ever exported percentiles — every later run's latency was
         * silently missing from Prometheus.
         */
        final LatencyHistogram histogram;

        /** Sized to the run so the acked bitset is exactly as large as needed. */
        final AckTracker ackTracker;

        final long runIdHash;

        volatile TaskStatus status = TaskStatus.PENDING;
        volatile long startTimeMs;
        volatile long endTimeMs;
        volatile String error;
        volatile Thread thread;
        volatile DataIntegrityVerifier verifier;
        volatile IntegrityResult integrityResult;
        volatile Map<String, Long> cdcPhaseDurations;
        volatile String currentPhase;
        /** Immutable terminal snapshot; every poll after the run is served from it. */
        volatile BenchmarkStatus finalStatus;

        /**
         * Why the first rejected send was rejected. Set once, from a Kafka
         * sender thread, and read when the task ends.
         */
        volatile String firstSendError;

        void recordSendError(Throwable t) {
            if (firstSendError == null) {
                firstSendError = describe(t);
            }
        }

        WorkerState(BenchmarkTask task) {
            this.task = task;
            String hashSource = task.getRunId() != null ? task.getRunId() : task.getTaskId();
            this.runIdHash = SequencedPayload.hashRunId(hashSource);
            this.histogram = new LatencyHistogram(task.getTaskId());
            this.ackTracker = new AckTracker(task.getMaxMessages());
        }

        /**
         * Frees what a finished run no longer needs: the ack bitset (up to
         * ~125 MB on a large integrity run) and the verifier's consumed set.
         * Must run only after {@link #finalStatus} has been captured.
         */
        void releaseHeavyState() {
            ackTracker.release();
            verifier = null;
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

            if (cdcPhaseDurations != null) {
                builder.phaseDurations(cdcPhaseDurations);
            }

            if (currentPhase != null) {
                builder.currentPhase(currentPhase);
            }

            return builder.build();
        }
    }
}
