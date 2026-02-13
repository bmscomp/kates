package com.klster.kates.engine;

import com.fasterxml.jackson.databind.JsonNode;
import com.klster.kates.domain.TestResult.TaskStatus;
import com.klster.kates.trogdor.TrogdorClient;
import com.klster.kates.trogdor.spec.TrogdorSpec;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.inject.Named;
import org.eclipse.microprofile.rest.client.inject.RestClient;

import java.util.logging.Level;
import java.util.logging.Logger;

/**
 * Backend that delegates benchmark execution to the Trogdor Coordinator.
 * Wraps the existing TrogdorClient and SpecFactory.
 */
@ApplicationScoped
@Named("trogdor")
public class TrogdorBackend implements BenchmarkBackend {

    private static final Logger LOG = Logger.getLogger(TrogdorBackend.class.getName());

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
        TrogdorClient.CreateTaskRequest request =
                new TrogdorClient.CreateTaskRequest(task.getTaskId(), spec);

        try {
            trogdorClient.createTask(request);
            LOG.info("Submitted Trogdor task: " + task.getTaskId());
            return new BenchmarkHandle(name(), task.getTaskId());
        } catch (Exception e) {
            LOG.log(Level.WARNING, "Failed to submit Trogdor task: " + task.getTaskId(), e);
            throw new BenchmarkException("Trogdor submission failed: " + e.getMessage(), e);
        }
    }

    @Override
    public BenchmarkStatus poll(BenchmarkHandle handle) {
        try {
            JsonNode taskStatus = trogdorClient.getTask(handle.taskId());
            return fromTrogdorStatus(taskStatus);
        } catch (Exception e) {
            LOG.log(Level.WARNING, "Failed to poll Trogdor task: " + handle.taskId(), e);
            return BenchmarkStatus.builder(TaskStatus.RUNNING).build();
        }
    }

    @Override
    public void stop(BenchmarkHandle handle) {
        try {
            trogdorClient.stopTask(handle.taskId());
        } catch (Exception e) {
            LOG.log(Level.WARNING, "Failed to stop Trogdor task: " + handle.taskId(), e);
        }
    }

    private TrogdorSpec toTrogdorSpec(BenchmarkTask task) {
        return switch (task.getWorkloadType()) {
            case PRODUCE -> com.klster.kates.trogdor.spec.ProduceBenchSpec.create(
                    resolveBootstrapServers(task),
                    task.getTopic(), task.getPartitions(),
                    task.getTargetMessagesPerSec(), task.getMaxMessages(),
                    task.getDurationMs(), task.getRecordSize());
            case CONSUME -> com.klster.kates.trogdor.spec.ConsumeBenchSpec.create(
                    resolveBootstrapServers(task),
                    task.getTopic(), task.getPartitions(),
                    task.getMaxMessages(), task.getDurationMs(),
                    task.getConsumerGroup());
            case ROUND_TRIP -> com.klster.kates.trogdor.spec.RoundTripWorkloadSpec.create(
                    resolveBootstrapServers(task),
                    task.getTopic(), task.getPartitions(),
                    task.getTargetMessagesPerSec(), task.getMaxMessages(),
                    task.getDurationMs(), task.getRecordSize());
        };
    }

    private String resolveBootstrapServers(BenchmarkTask task) {
        String servers = task.getProducerConfig().get("bootstrap.servers");
        return servers != null ? servers : "localhost:9092";
    }

    private BenchmarkStatus fromTrogdorStatus(JsonNode taskStatus) {
        if (taskStatus == null) {
            return BenchmarkStatus.builder(TaskStatus.RUNNING).build();
        }

        String state = taskStatus.path("state").asText("");
        TaskStatus status = switch (state) {
            case "PENDING" -> TaskStatus.PENDING;
            case "RUNNING" -> TaskStatus.RUNNING;
            case "STOPPING" -> TaskStatus.STOPPING;
            case "DONE" -> TaskStatus.DONE;
            default -> TaskStatus.RUNNING;
        };

        JsonNode metrics = taskStatus.path("status");
        BenchmarkStatus.Builder builder = BenchmarkStatus.builder(status);

        if (!metrics.isMissingNode()) {
            double elapsedSec = Math.max(1, metrics.path("elapsedMs").asDouble(1) / 1000.0);
            long totalSent = metrics.path("totalSent").asLong(0);

            builder.recordsProcessed(totalSent)
                   .throughputRecordsPerSec(totalSent / elapsedSec)
                   .avgLatencyMs(metrics.path("averageLatencyMs").asDouble(0))
                   .p50LatencyMs(metrics.path("p50LatencyMs").asDouble(0))
                   .p95LatencyMs(metrics.path("p95LatencyMs").asDouble(0))
                   .p99LatencyMs(metrics.path("p99LatencyMs").asDouble(0))
                   .maxLatencyMs(metrics.path("maxLatencyMs").asDouble(0));

            String error = metrics.path("error").asText(null);
            if (error != null && !error.isEmpty()) {
                builder.error(error);
            }
        }

        return builder.build();
    }
}
