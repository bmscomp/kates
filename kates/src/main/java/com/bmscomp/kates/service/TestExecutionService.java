package com.bmscomp.kates.service;

import java.time.Instant;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import com.fasterxml.jackson.databind.JsonNode;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.jboss.logging.Logger;

import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.trogdor.SpecFactory;
import com.bmscomp.kates.trogdor.TrogdorClient;
import com.bmscomp.kates.trogdor.spec.TrogdorSpec;

@ApplicationScoped
public class TestExecutionService {

    private static final Logger LOG = Logger.getLogger(TestExecutionService.class);

    private final SpecFactory specFactory;
    private final TopicService topicService;
    private final TestRunRepository repository;
    private final TrogdorClient trogdorClient;

    @Inject
    public TestExecutionService(
            SpecFactory specFactory,
            TopicService topicService,
            TestRunRepository repository,
            @RestClient TrogdorClient trogdorClient) {
        this.specFactory = specFactory;
        this.topicService = topicService;
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
            run = run.withStatus(TestResult.TaskStatus.RUNNING);

            for (int i = 0; i < trogdorSpecs.size(); i++) {
                String taskId = run.getId() + "-" + type.name().toLowerCase() + "-" + i;
                TrogdorSpec trogdorSpec = trogdorSpecs.get(i);

                TrogdorClient.CreateTaskRequest taskReq = new TrogdorClient.CreateTaskRequest(taskId, trogdorSpec);

                try {
                    trogdorClient.createTask(taskReq);
                    TestResult result = new TestResult()
                            .withTaskId(taskId)
                            .withTestType(type)
                            .withStatus(TestResult.TaskStatus.RUNNING)
                            .withStartTime(Instant.now().toString());
                    run = run.withAddedResult(result);
                    LOG.info("Submitted Trogdor task: " + taskId);
                } catch (Exception e) {
                    LOG.warn("Failed to submit Trogdor task: " + taskId, e);
                    TestResult failedResult = new TestResult()
                            .withTaskId(taskId)
                            .withTestType(type)
                            .withStatus(TestResult.TaskStatus.FAILED)
                            .withError(e.getMessage())
                            .withStartTime(Instant.now().toString())
                            .withEndTime(Instant.now().toString());
                    run = run.withAddedResult(failedResult);
                }
            }

            boolean allFailed = run.getResults().stream().allMatch(r -> r.getStatus() == TestResult.TaskStatus.FAILED);
            if (allFailed) {
                run = run.withStatus(TestResult.TaskStatus.FAILED);
            }

        } catch (Exception e) {
            LOG.error("Test execution failed for run: " + run.getId(), e);
            run = run.withStatus(TestResult.TaskStatus.FAILED);
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
                    result = updateResultFromTrogdor(result, taskStatus);
                    run = run.withUpdatedResult(result);
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
            run = run.withStatus(anyFailed ? TestResult.TaskStatus.FAILED : TestResult.TaskStatus.DONE);
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
                    result = result.withStatus(TestResult.TaskStatus.STOPPING);
                    run = run.withUpdatedResult(result);
                } catch (Exception e) {
                    LOG.warn("Failed to stop task: " + result.getTaskId(), e);
                }
            }
        }

        run = run.withStatus(TestResult.TaskStatus.STOPPING);
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

        topicService.createTopic(topicName, spec.getPartitions(), spec.getReplicationFactor(), topicConfig);

        if (type == TestType.VOLUME) {
            topicService.createTopic(
                    topicName + "-large", spec.getPartitions(), spec.getReplicationFactor(), topicConfig);
            topicService.createTopic(
                    topicName + "-count", spec.getPartitions(), spec.getReplicationFactor(), topicConfig);
        }
    }

    private TestResult updateResultFromTrogdor(TestResult result, JsonNode taskStatus) {
        if (taskStatus == null) return result;

        String state = taskStatus.path("state").asText("");
        result = result.withStatus(
                switch (state) {
                    case "PENDING" -> TestResult.TaskStatus.PENDING;
                    case "RUNNING" -> TestResult.TaskStatus.RUNNING;
                    case "STOPPING" -> TestResult.TaskStatus.STOPPING;
                    case "DONE" -> TestResult.TaskStatus.DONE;
                    default -> result.getStatus();
                });

        JsonNode status = taskStatus.path("status");
        if (!status.isMissingNode()) {
            result = result.withThroughputRecordsPerSec(status.path("totalSent").asDouble(0)
                    / Math.max(1, status.path("elapsedMs").asDouble(1) / 1000.0))
                .withAvgLatencyMs(status.path("averageLatencyMs").asDouble(0))
                .withP50LatencyMs(status.path("p50LatencyMs").asDouble(0))
                .withP95LatencyMs(status.path("p95LatencyMs").asDouble(0))
                .withP99LatencyMs(status.path("p99LatencyMs").asDouble(0))
                .withMaxLatencyMs(status.path("maxLatencyMs").asDouble(0))
                .withRecordsSent(status.path("totalSent").asLong(0));

            if (result.getStatus() == TestResult.TaskStatus.DONE) {
                result = result.withEndTime(Instant.now().toString());
            }

            String error = status.path("error").asText(null);
            if (error != null && !error.isEmpty()) {
                result = result.withError(error)
                               .withStatus(TestResult.TaskStatus.FAILED);
            }
        }
        return result;
    }
}
