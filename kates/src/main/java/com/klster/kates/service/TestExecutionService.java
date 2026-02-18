package com.klster.kates.service;

import java.time.Instant;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import com.fasterxml.jackson.databind.JsonNode;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.jboss.logging.Logger;

import com.klster.kates.domain.CreateTestRequest;
import com.klster.kates.domain.TestResult;
import com.klster.kates.domain.TestRun;
import com.klster.kates.domain.TestSpec;
import com.klster.kates.domain.TestType;
import com.klster.kates.trogdor.SpecFactory;
import com.klster.kates.trogdor.TrogdorClient;
import com.klster.kates.trogdor.spec.TrogdorSpec;

@ApplicationScoped
public class TestExecutionService {

    private static final Logger LOG = Logger.getLogger(TestExecutionService.class);

    private final SpecFactory specFactory;
    private final KafkaAdminService kafkaAdmin;
    private final TestRunRepository repository;
    private final TrogdorClient trogdorClient;

    @Inject
    public TestExecutionService(
            SpecFactory specFactory,
            KafkaAdminService kafkaAdmin,
            TestRunRepository repository,
            @RestClient TrogdorClient trogdorClient) {
        this.specFactory = specFactory;
        this.kafkaAdmin = kafkaAdmin;
        this.repository = repository;
        this.trogdorClient = trogdorClient;
    }

    public TestRun executeTest(CreateTestRequest request) {
        TestType type = request.getType();
        TestSpec spec = request.getSpec() != null ? request.getSpec() : new TestSpec();

        TestRun run = new TestRun(type, spec);
        repository.save(run);

        try {
            createTestTopic(spec, type);

            List<TrogdorSpec> trogdorSpecs = specFactory.buildSpecs(type, spec, run.getId());
            run.setStatus(TestResult.TaskStatus.RUNNING);

            for (int i = 0; i < trogdorSpecs.size(); i++) {
                String taskId = run.getId() + "-" + type.name().toLowerCase() + "-" + i;
                TrogdorSpec trogdorSpec = trogdorSpecs.get(i);

                TrogdorClient.CreateTaskRequest taskReq = new TrogdorClient.CreateTaskRequest(taskId, trogdorSpec);

                try {
                    trogdorClient.createTask(taskReq);
                    TestResult result = new TestResult();
                    result.setTaskId(taskId);
                    result.setTestType(type);
                    result.setStatus(TestResult.TaskStatus.RUNNING);
                    result.setStartTime(Instant.now().toString());
                    run.addResult(result);
                    LOG.info("Submitted Trogdor task: " + taskId);
                } catch (Exception e) {
                    LOG.warn("Failed to submit Trogdor task: " + taskId, e);
                    TestResult failedResult = new TestResult();
                    failedResult.setTaskId(taskId);
                    failedResult.setTestType(type);
                    failedResult.setStatus(TestResult.TaskStatus.FAILED);
                    failedResult.setError(e.getMessage());
                    failedResult.setStartTime(Instant.now().toString());
                    failedResult.setEndTime(Instant.now().toString());
                    run.addResult(failedResult);
                }
            }

            boolean allFailed = run.getResults().stream().allMatch(r -> r.getStatus() == TestResult.TaskStatus.FAILED);
            if (allFailed) {
                run.setStatus(TestResult.TaskStatus.FAILED);
            }

        } catch (Exception e) {
            LOG.error("Test execution failed for run: " + run.getId(), e);
            run.setStatus(TestResult.TaskStatus.FAILED);
        }

        repository.save(run);
        return run;
    }

    public TestRun refreshStatus(String runId) {
        TestRun run = repository
                .findById(runId)
                .orElseThrow(() -> new IllegalArgumentException("Test run not found: " + runId));

        boolean allDone = true;
        boolean anyFailed = false;

        for (TestResult result : run.getResults()) {
            if (result.getStatus() == TestResult.TaskStatus.RUNNING
                    || result.getStatus() == TestResult.TaskStatus.PENDING) {
                try {
                    JsonNode taskStatus = trogdorClient.getTask(result.getTaskId());
                    updateResultFromTrogdor(result, taskStatus);
                } catch (Exception e) {
                    LOG.warn("Failed to fetch status for task: " + result.getTaskId(), e);
                }
            }

            if (result.getStatus() != TestResult.TaskStatus.DONE
                    && result.getStatus() != TestResult.TaskStatus.FAILED) {
                allDone = false;
            }
            if (result.getStatus() == TestResult.TaskStatus.FAILED) {
                anyFailed = true;
            }
        }

        if (allDone) {
            run.setStatus(anyFailed ? TestResult.TaskStatus.FAILED : TestResult.TaskStatus.DONE);
        }

        repository.save(run);
        return run;
    }

    public void stopTest(String runId) {
        TestRun run = repository
                .findById(runId)
                .orElseThrow(() -> new IllegalArgumentException("Test run not found: " + runId));

        for (TestResult result : run.getResults()) {
            if (result.getStatus() == TestResult.TaskStatus.RUNNING) {
                try {
                    trogdorClient.stopTask(result.getTaskId());
                    result.setStatus(TestResult.TaskStatus.STOPPING);
                } catch (Exception e) {
                    LOG.warn("Failed to stop task: " + result.getTaskId(), e);
                }
            }
        }

        run.setStatus(TestResult.TaskStatus.STOPPING);
        repository.save(run);
    }

    private void createTestTopic(TestSpec spec, TestType type) {
        String topicName =
                spec.getTopic() != null ? spec.getTopic() : type.name().toLowerCase() + "-test";
        Map<String, String> topicConfig = new HashMap<>();
        topicConfig.put("min.insync.replicas", String.valueOf(spec.getMinInsyncReplicas()));

        if (type == TestType.VOLUME) {
            topicConfig.put("retention.ms", "1800000");
            topicConfig.put("max.message.bytes", "1048576");
        }

        kafkaAdmin.createTopic(topicName, spec.getPartitions(), spec.getReplicationFactor(), topicConfig);

        if (type == TestType.VOLUME) {
            kafkaAdmin.createTopic(
                    topicName + "-large", spec.getPartitions(), spec.getReplicationFactor(), topicConfig);
            kafkaAdmin.createTopic(
                    topicName + "-count", spec.getPartitions(), spec.getReplicationFactor(), topicConfig);
        }
    }

    private void updateResultFromTrogdor(TestResult result, JsonNode taskStatus) {
        if (taskStatus == null) return;

        String state = taskStatus.path("state").asText("");
        result.setStatus(
                switch (state) {
                    case "PENDING" -> TestResult.TaskStatus.PENDING;
                    case "RUNNING" -> TestResult.TaskStatus.RUNNING;
                    case "STOPPING" -> TestResult.TaskStatus.STOPPING;
                    case "DONE" -> TestResult.TaskStatus.DONE;
                    default -> result.getStatus();
                });

        JsonNode status = taskStatus.path("status");
        if (!status.isMissingNode()) {
            result.setThroughputRecordsPerSec(status.path("totalSent").asDouble(0)
                    / Math.max(1, status.path("elapsedMs").asDouble(1) / 1000.0));
            result.setAvgLatencyMs(status.path("averageLatencyMs").asDouble(0));
            result.setP50LatencyMs(status.path("p50LatencyMs").asDouble(0));
            result.setP95LatencyMs(status.path("p95LatencyMs").asDouble(0));
            result.setP99LatencyMs(status.path("p99LatencyMs").asDouble(0));
            result.setMaxLatencyMs(status.path("maxLatencyMs").asDouble(0));
            result.setRecordsSent(status.path("totalSent").asLong(0));

            if (result.getStatus() == TestResult.TaskStatus.DONE) {
                result.setEndTime(Instant.now().toString());
            }

            String error = status.path("error").asText(null);
            if (error != null && !error.isEmpty()) {
                result.setError(error);
                result.setStatus(TestResult.TaskStatus.FAILED);
            }
        }
    }
}
