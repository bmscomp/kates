package com.bmscomp.kates.engine;

import java.util.HashMap;
import java.util.Map;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.inject.Named;

import com.fasterxml.jackson.databind.JsonNode;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.jboss.logging.Logger;

import com.bmscomp.kates.domain.TestResult.TaskStatus;
import com.bmscomp.kates.trogdor.TrogdorClient;
import com.bmscomp.kates.trogdor.spec.ConsumeBenchSpec;
import com.bmscomp.kates.trogdor.spec.ProduceBenchSpec;
import com.bmscomp.kates.trogdor.spec.RoundTripWorkloadSpec;
import com.bmscomp.kates.trogdor.spec.TrogdorSpec;

/**
 * Backend that delegates benchmark execution to the Trogdor Coordinator.
 * Wraps the existing TrogdorClient and SpecFactory.
 */
@ApplicationScoped
@Named("trogdor")
public class TrogdorBackend implements BenchmarkBackend {

    private static final Logger LOG = Logger.getLogger(TrogdorBackend.class);

    private final TrogdorClient trogdorClient;

    @Inject
    public TrogdorBackend(@RestClient TrogdorClient trogdorClient) {
        this.trogdorClient = trogdorClient;
    }

    @Override
    public String name() {
        return "trogdor";
    }

    @Override
    public BenchmarkHandle submit(BenchmarkTask task) {
        TrogdorSpec spec = toTrogdorSpec(task);
        TrogdorClient.CreateTaskRequest request = new TrogdorClient.CreateTaskRequest(task.getTaskId(), spec);

        try {
            trogdorClient.createTask(request);
            LOG.info("Submitted Trogdor task: " + task.getTaskId());
            return new BenchmarkHandle(name(), task.getTaskId());
        } catch (Exception e) {
            LOG.warn("Failed to submit Trogdor task: " + task.getTaskId(), e);
            throw new BenchmarkException("Trogdor submission failed: " + e.getMessage(), e);
        }
    }

    @Override
    public BenchmarkStatus poll(BenchmarkHandle handle) {
        try {
            JsonNode taskStatus = trogdorClient.getTask(handle.taskId());
            return fromTrogdorStatus(taskStatus, System.currentTimeMillis());
        } catch (Exception e) {
            LOG.warn("Failed to poll Trogdor task: " + handle.taskId(), e);
            return BenchmarkStatus.builder(TaskStatus.RUNNING).build();
        }
    }

    @Override
    public void stop(BenchmarkHandle handle) {
        try {
            trogdorClient.stopTask(handle.taskId());
        } catch (Exception e) {
            LOG.warn("Failed to stop Trogdor task: " + handle.taskId(), e);
        }
    }

    // Package-private: what a task becomes on Trogdor is the whole of this
    // backend's work, and checking it through submit would need a coordinator.
    TrogdorSpec toTrogdorSpec(BenchmarkTask task) {
        return switch (task.getWorkloadType()) {
            case PRODUCE -> {
                var produce = com.bmscomp.kates.trogdor.spec.ProduceBenchSpec.create(
                        resolveBootstrapServers(task),
                        task.getTopic(),
                        task.getPartitions(),
                        task.getTargetMessagesPerSec(),
                        task.getMaxMessages(),
                        task.getDurationMs(),
                        task.getRecordSize());
                produce.getProducerConf().putAll(clientConfig(task.getProducerConfig()));
                yield produce;
            }
            case CONSUME -> {
                var consume = com.bmscomp.kates.trogdor.spec.ConsumeBenchSpec.create(
                        resolveBootstrapServers(task),
                        task.getTopic(),
                        task.getPartitions(),
                        task.getMaxMessages(),
                        task.getDurationMs(),
                        task.getConsumerGroup());
                consume.getConsumerConf().putAll(clientConfig(task.getConsumerConfig()));
                yield consume;
            }
            case ROUND_TRIP -> {
                var roundTrip = com.bmscomp.kates.trogdor.spec.RoundTripWorkloadSpec.create(
                        resolveBootstrapServers(task),
                        task.getTopic(),
                        task.getPartitions(),
                        task.getTargetMessagesPerSec(),
                        task.getMaxMessages(),
                        task.getDurationMs(),
                        task.getRecordSize());
                roundTrip.getProducerConf().putAll(clientConfig(task.getProducerConfig()));
                roundTrip.getConsumerConf().putAll(clientConfig(task.getConsumerConfig()));
                yield roundTrip;
            }
            case INTEGRITY, INTEGRITY_CDC ->
                throw new BenchmarkException("INTEGRITY/CDC tests require the native backend", null);
        };
    }

    /**
     * A task's client settings, for a Trogdor spec's producerConf or
     * consumerConf. They were left out entirely, so a run on this backend used
     * the Kafka client's defaults whatever the request's acks, batching,
     * compression, idempotence or fetch settings said. The bootstrap servers
     * have a field of their own in the spec.
     */
    private static Map<String, String> clientConfig(Map<String, String> config) {
        Map<String, String> conf = new HashMap<>(config);
        conf.remove("bootstrap.servers");
        return conf;
    }

    private String resolveBootstrapServers(BenchmarkTask task) {
        String servers = task.getProducerConfig().get("bootstrap.servers");
        return servers != null ? servers : "localhost:9092";
    }

    /**
     * A coordinator task state as a status. The state holds the task's spec,
     * the coordinator's {@code startedMs} and, once DONE, {@code doneMs} and
     * {@code error}; {@code status} is what the task's worker last reported,
     * and that differs by workload:
     *
     * <ul>
     *   <li>ProduceBench: {@code totalSent}, and record latency as an average,
     *       p50, p95 and p99. There is no maximum.
     *   <li>ConsumeBench: one entry per consumer client, each with its
     *       {@code totalMessagesReceived}. Its latency figures are the time
     *       between poll batches, not a record's latency, so none is reported,
     *       as the native backend reports none for a consumer.
     *   <li>RoundTrip: {@code totalUniqueSent} and {@code totalReceived}, the
     *       records that made the round trip. No latency.
     * </ul>
     *
     * <p>It used to read {@code totalSent}, {@code elapsedMs} and
     * {@code maxLatencyMs} for every workload. No worker reports the last two,
     * so throughput was the record count itself and the maximum was always 0,
     * and consume and round-trip tasks showed no records at all.
     *
     * <p>No worker reports how long it ran, so throughput is the records over
     * the time since the coordinator started the task: to {@code doneMs} once
     * it is DONE, and to {@code nowMs} while it runs. Workers refresh their
     * status every 30 s (produce, round trip) or 60 s (consume), so a running
     * task's rate trails its real one; the DONE poll's is the one a result
     * keeps. A consume task that stops at its message count is only seen to
     * finish by a check that runs once a minute, so its rate is understated
     * by up to that minute.
     */
    // Package-private and static: the status fixtures in TrogdorBackendTest
    // are the only coordinator these mappings can be checked against.
    static BenchmarkStatus fromTrogdorStatus(JsonNode taskState, long nowMs) {
        if (taskState == null) {
            return BenchmarkStatus.builder(TaskStatus.RUNNING).build();
        }

        // A task's error is on its state, never in the worker's status. The
        // coordinator sets it when a worker aborts or the task cannot start.
        String error = taskState.path("error").asText("");
        String state = taskState.path("state").asText("");
        TaskStatus status =
                switch (state) {
                    case "PENDING" -> TaskStatus.PENDING;
                    case "RUNNING" -> TaskStatus.RUNNING;
                    case "STOPPING" -> TaskStatus.STOPPING;
                    case "DONE" -> error.isEmpty() ? TaskStatus.DONE : TaskStatus.FAILED;
                    default -> TaskStatus.RUNNING;
                };

        BenchmarkStatus.Builder builder = BenchmarkStatus.builder(status);
        if (!error.isEmpty()) {
            builder.error(error);
        }

        JsonNode worker = taskState.path("status");
        long records =
                switch (taskState.path("spec").path("class").asText("")) {
                    case ProduceBenchSpec.CLASS_NAME -> {
                        builder.avgLatencyMs(worker.path("averageLatencyMs").asDouble(0))
                                .p50LatencyMs(worker.path("p50LatencyMs").asDouble(0))
                                .p95LatencyMs(worker.path("p95LatencyMs").asDouble(0))
                                .p99LatencyMs(worker.path("p99LatencyMs").asDouble(0));
                        yield worker.path("totalSent").asLong(0);
                    }
                    case ConsumeBenchSpec.CLASS_NAME -> {
                        long received = 0;
                        for (JsonNode consumer : worker) {
                            received += consumer.path("totalMessagesReceived").asLong(0);
                        }
                        yield received;
                    }
                    case RoundTripWorkloadSpec.CLASS_NAME ->
                        worker.path("totalReceived").asLong(0);
                    default -> 0;
                };

        // A task that never started (PENDING, or DONE because it could not)
        // has no startedMs.
        long startedMs = taskState.path("startedMs").asLong(-1);
        long endMs = "DONE".equals(state) ? taskState.path("doneMs").asLong(-1) : nowMs;
        double throughput = startedMs > 0 && endMs > startedMs ? records / ((endMs - startedMs) / 1000.0) : 0;

        return builder.recordsProcessed(records)
                .throughputRecordsPerSec(throughput)
                .build();
    }
}
