package com.bmscomp.kates.engine;

import java.time.Instant;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.Semaphore;
import jakarta.annotation.PostConstruct;
import jakarta.annotation.PreDestroy;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.event.Event;
import jakarta.enterprise.inject.Any;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;

import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import com.bmscomp.kates.config.TestTypeDefaults;
import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.ScenarioPhase;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestScenario;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.export.LatencyHeatmapData;
import com.bmscomp.kates.service.TopicService;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.webhook.WebhookService;

/**
 * Orchestrator that routes benchmark execution to pluggable backends.
 * Applies per-test-type defaults from configuration before building tasks.
 */
@ApplicationScoped
public class TestOrchestrator {

    private static final Logger LOG = Logger.getLogger(TestOrchestrator.class);

    private final TopicService topicService;
    private final TestRunRepository repository;
    private final Instance<BenchmarkBackend> backends;
    private final TestTypeDefaults typeDefaults;
    private final BenchmarkMetrics benchmarkMetrics;
    private final KatesMetrics katesMetrics;
    private final WebhookService webhookService;
    private final Event<TestLifecycleEvent> lifecycleEvents;
    private final String defaultBackend;
    private final String bootstrapServers;
    private final int maxConcurrentTests;
    private final Semaphore concurrencyGuard;
    private final Map<String, List<BenchmarkHandle>> activeHandles = new ConcurrentHashMap<>();
    private final Map<String, List<LatencyHeatmapData.HeatmapRow>> heatmapRows = new ConcurrentHashMap<>();

    @Inject
    public TestOrchestrator(
            TopicService topicService,
            TestRunRepository repository,
            @Any Instance<BenchmarkBackend> backends,
            TestTypeDefaults typeDefaults,
            BenchmarkMetrics benchmarkMetrics,
            KatesMetrics katesMetrics,
            WebhookService webhookService,
            Event<TestLifecycleEvent> lifecycleEvents,
            @ConfigProperty(name = "kates.engine.default-backend", defaultValue = "native") String defaultBackend,
            @ConfigProperty(name = "kates.kafka.bootstrap-servers") String bootstrapServers,
            @ConfigProperty(name = "kates.engine.max-concurrent-tests", defaultValue = "3") int maxConcurrentTests) {
        this.topicService = topicService;
        this.repository = repository;
        this.backends = backends;
        this.typeDefaults = typeDefaults;
        this.benchmarkMetrics = benchmarkMetrics;
        this.katesMetrics = katesMetrics;
        this.webhookService = webhookService;
        this.lifecycleEvents = lifecycleEvents;
        this.defaultBackend = defaultBackend;
        this.bootstrapServers = bootstrapServers;
        this.maxConcurrentTests = maxConcurrentTests;
        this.concurrencyGuard = new Semaphore(maxConcurrentTests);
    }

    @PostConstruct
    void recoverOrphans() {
        List<TestRun> orphans = repository.findByStatus(TestResult.TaskStatus.RUNNING);
        if (orphans.isEmpty()) {
            return;
        }
        LOG.infof("Recovering %d orphaned RUNNING tests from previous lifecycle", orphans.size());
        for (TestRun run : orphans) {
            run = run.withStatus(TestResult.TaskStatus.FAILED);
            List<TestResult> newResults = new java.util.ArrayList<>();
            for (TestResult result : run.getResults()) {
                if (result.getStatus() == TestResult.TaskStatus.RUNNING) {
                    result = result.withStatus(TestResult.TaskStatus.FAILED)
                                   .withError("Recovered: test was orphaned after server restart");
                }
                newResults.add(result);
            }
            run = run.withResults(newResults);
            repository.save(run);
            LOG.infof("  Marked test %s as FAILED (orphan recovery)", run.getId());
        }
    }

    public com.bmscomp.kates.util.Result<TestRun, Exception> executeTest(CreateTestRequest request) {
        if (request.isScenario()) {
            return executeScenario(request);
        }

        if (!concurrencyGuard.tryAcquire()) {
            return com.bmscomp.kates.util.Result.failure(new BenchmarkException(
                    "Concurrency limit reached: " + maxConcurrentTests
                    + " tests already running. Retry later or increase kates.engine.max-concurrent-tests."));
        }

        TestType type = request.getType();
        TestSpec spec = applyTypeDefaults(type, request.getSpec());
        String backendName = request.getBackend() != null ? request.getBackend() : defaultBackend;

        com.bmscomp.kates.util.Result<BenchmarkBackend, Exception> backendResult = resolveBackend(backendName);
        if (backendResult.isFailure()) {
            concurrencyGuard.release();
            return com.bmscomp.kates.util.Result.failure(backendResult.asFailure().orElseThrow());
        }
        BenchmarkBackend backend = backendResult.asSuccess().orElseThrow();

        TestRun run = new TestRun(type, spec).withBackend(backendName);
        repository.save(run);
        fireEvent(run, TestLifecycleEvent.EventKind.CREATED);

        Thread.startVirtualThread(() -> {
            try {
                executeAsync(run, type, spec, backendName, backend);
            } finally {
                concurrencyGuard.release();
            }
        });

        return com.bmscomp.kates.util.Result.success(run);
    }

    private void executeAsync(TestRun run, TestType type, TestSpec spec, String backendName, BenchmarkBackend backend) {
        org.jboss.logging.MDC.put("runId", run.getId());
        org.jboss.logging.MDC.put("testType", type.name());
        org.jboss.logging.MDC.put("backend", backendName);
        try {
            createTestTopic(spec, type);
            List<BenchmarkTask> tasks = buildTasks(type, spec, run.getId());
            run = run.withStatus(TestResult.TaskStatus.RUNNING);
            fireEvent(run, TestLifecycleEvent.EventKind.RUNNING);
            benchmarkMetrics.startRun(run.getId(), type.name(), backendName);

            var handles = new java.util.ArrayList<BenchmarkHandle>();

            for (BenchmarkTask task : tasks) {
                try {
                    BenchmarkHandle handle = backend.submit(task);
                    handles.add(handle);

                    TestResult result = new TestResult()
                            .withTaskId(task.getTaskId())
                            .withTestType(type)
                            .withStatus(TestResult.TaskStatus.RUNNING)
                            .withStartTime(Instant.now().toString());
                    run = run.withAddedResult(result);
                    LOG.info("Submitted task via " + backendName + ": " + task.getTaskId());
                } catch (Exception e) {
                    LOG.warn("Failed to submit task: " + task.getTaskId(), e);
                    TestResult failedResult = new TestResult()
                            .withTaskId(task.getTaskId())
                            .withTestType(type)
                            .withStatus(TestResult.TaskStatus.FAILED)
                            .withError(e.getMessage())
                            .withStartTime(Instant.now().toString())
                            .withEndTime(Instant.now().toString());
                    run = run.withAddedResult(failedResult);
                }
            }

            activeHandles.put(run.getId(), handles);

            boolean allFailed = run.getResults().stream().allMatch(r -> r.getStatus() == TestResult.TaskStatus.FAILED);
            if (allFailed) {
                run = run.withStatus(TestResult.TaskStatus.FAILED);
            }

        } catch (Exception e) {
            LOG.error("Test execution failed for run: " + run.getId(), e);
            run = run.withStatus(TestResult.TaskStatus.FAILED);
        }

        repository.save(run);
        if (run.getStatus() == TestResult.TaskStatus.FAILED) {
            fireEvent(run, TestLifecycleEvent.EventKind.FAILED);
            benchmarkMetrics.endRun(run.getId());
        } else if (run.getStatus() == TestResult.TaskStatus.DONE) {
            fireEvent(run, TestLifecycleEvent.EventKind.DONE);
            benchmarkMetrics.endRun(run.getId());
        }
        org.jboss.logging.MDC.remove("runId");
        org.jboss.logging.MDC.remove("testType");
        org.jboss.logging.MDC.remove("backend");
    }

    /**
     * Executes a multi-phase scenario: each phase runs sequentially,
     * using the resolved spec (base + phase overrides + type defaults).
     */
    com.bmscomp.kates.util.Result<TestRun, Exception> executeScenario(CreateTestRequest request) {
        TestScenario scenario = request.getScenario();
        TestType type = scenario.getType() != null ? scenario.getType() : request.getType();
        String backendName = scenario.getBackend() != null
                ? scenario.getBackend()
                : (request.getBackend() != null ? request.getBackend() : defaultBackend);

        com.bmscomp.kates.util.Result<BenchmarkBackend, Exception> backendResult = resolveBackend(backendName);
        if (backendResult.isFailure()) {
            return com.bmscomp.kates.util.Result.failure(backendResult.asFailure().orElseThrow());
        }
        BenchmarkBackend backend = backendResult.asSuccess().orElseThrow();

        TestSpec baseSpec = applyTypeDefaults(type, scenario.getBaseSpec());
        TestRun run = new TestRun(type, baseSpec)
            .withBackend(backendName)
            .withScenarioName(scenario.getName())
            .withLabels(scenario.getLabels())
            .withSla(scenario.getSla())
            .withStatus(TestResult.TaskStatus.RUNNING);
        repository.save(run);
        fireEvent(run, TestLifecycleEvent.EventKind.CREATED);
        fireEvent(run, TestLifecycleEvent.EventKind.RUNNING);

        try {
            createTestTopic(baseSpec, type);
            benchmarkMetrics.startRun(run.getId(), type.name(), backendName);

            var allHandles = new java.util.ArrayList<BenchmarkHandle>();

            for (int phaseIdx = 0; phaseIdx < scenario.getPhases().size(); phaseIdx++) {
                ScenarioPhase phase = scenario.getPhases().get(phaseIdx);
                String phaseName = phase.getName() != null ? phase.getName() : "phase-" + phaseIdx;
                TestSpec phaseSpec = scenario.resolveSpecForPhase(phase);

                List<BenchmarkTask> tasks = buildPhaseTask(phase, phaseSpec, type, run.getId(), phaseName);

                for (BenchmarkTask task : tasks) {
                    try {
                        BenchmarkHandle handle = backend.submit(task);
                        allHandles.add(handle);

                        TestResult result = new TestResult()
                                .withTaskId(task.getTaskId())
                                .withTestType(type)
                                .withStatus(TestResult.TaskStatus.RUNNING)
                                .withStartTime(Instant.now().toString())
                                .withPhaseName(phaseName);
                        run = run.withAddedResult(result);
                        LOG.info("Scenario phase [" + phaseName + "] submitted: " + task.getTaskId());
                    } catch (Exception e) {
                        LOG.warn("Phase [" + phaseName + "] failed to submit: " + task.getTaskId(), e);
                        TestResult failedResult = new TestResult()
                                .withTaskId(task.getTaskId())
                                .withTestType(type)
                                .withStatus(TestResult.TaskStatus.FAILED)
                                .withError(e.getMessage())
                                .withStartTime(Instant.now().toString())
                                .withEndTime(Instant.now().toString())
                                .withPhaseName(phaseName);
                        run = run.withAddedResult(failedResult);
                    }
                }
            }

            activeHandles.put(run.getId(), allHandles);

            boolean allFailed = run.getResults().stream().allMatch(r -> r.getStatus() == TestResult.TaskStatus.FAILED);
            if (allFailed) {
                run = run.withStatus(TestResult.TaskStatus.FAILED);
            }

        } catch (Exception e) {
            LOG.error("Scenario execution failed for run: " + run.getId(), e);
            run = run.withStatus(TestResult.TaskStatus.FAILED);
        }

        repository.save(run);
        if (run.getStatus() == TestResult.TaskStatus.FAILED) {
            fireEvent(run, TestLifecycleEvent.EventKind.FAILED);
            benchmarkMetrics.endRun(run.getId());
        } else if (run.getStatus() == TestResult.TaskStatus.DONE) {
            fireEvent(run, TestLifecycleEvent.EventKind.DONE);
            benchmarkMetrics.endRun(run.getId());
        }
        return com.bmscomp.kates.util.Result.success(run);
    }

    public TestRun refreshStatus(String runId) {
        TestRun run = repository
                .findById(runId)
                .orElseThrow(() -> new IllegalArgumentException("Test run not found: " + runId));

        String backendName = run.getBackend() != null ? run.getBackend() : defaultBackend;
        com.bmscomp.kates.util.Result<BenchmarkBackend, Exception> backendResult = resolveBackend(backendName);
        if (backendResult.isFailure()) {
            return run; // Cannot poll status if backend is missing.
        }
        BenchmarkBackend backend = backendResult.asSuccess().orElseThrow();

        List<BenchmarkHandle> handles = activeHandles.getOrDefault(runId, List.of());
        Map<String, BenchmarkHandle> handleMap = new HashMap<>();
        for (BenchmarkHandle h : handles) {
            handleMap.put(h.taskId(), h);
        }

        boolean allDone = true;
        boolean anyFailed = false;

        List<TestResult> updatedResults = new java.util.ArrayList<>();
        for (TestResult result : run.getResults()) {
            if (result.getStatus() == TestResult.TaskStatus.RUNNING
                    || result.getStatus() == TestResult.TaskStatus.PENDING) {
                BenchmarkHandle handle = handleMap.get(result.getTaskId());
                if (handle != null) {
                    try {
                        BenchmarkStatus status = backend.poll(handle);
                        result = applyStatus(result, status);

                        if (status.getHeatmapBuckets() != null) {
                            heatmapRows
                                    .computeIfAbsent(runId, k -> new java.util.ArrayList<>())
                                    .add(new LatencyHeatmapData.HeatmapRow(
                                            System.currentTimeMillis(),
                                            result.getPhaseName(),
                                            status.getHeatmapBuckets()));
                        }
                    } catch (Exception e) {
                        LOG.warn("Failed to poll task: " + result.getTaskId(), e);
                    }
                }
            }

            if (result.getStatus() != TestResult.TaskStatus.DONE
                    && result.getStatus() != TestResult.TaskStatus.FAILED) {
                allDone = false;
            }
            if (result.getStatus() == TestResult.TaskStatus.FAILED) {
                anyFailed = true;
            }
            updatedResults.add(result);
        }
        run = run.withResults(updatedResults);

        if (allDone) {
            run = run.withStatus(anyFailed ? TestResult.TaskStatus.FAILED : TestResult.TaskStatus.DONE);
            activeHandles.remove(runId);
            fireEvent(run, anyFailed ? TestLifecycleEvent.EventKind.FAILED : TestLifecycleEvent.EventKind.DONE);

            String typeName = run.getTestType() != null ? run.getTestType().name() : "UNKNOWN";
            String outcome = anyFailed ? "failed" : "done";
            katesMetrics.recordTestCompleted(typeName, outcome);
            webhookService.fireTestCompleted(run);

            if (run.getCreatedAt() != null) {
                try {
                    var start = java.time.Instant.parse(run.getCreatedAt());
                    katesMetrics.recordTestDuration(
                            typeName, java.time.Duration.between(start, java.time.Instant.now()));
                } catch (Exception ignored) {
                }
            }

            for (TestResult r : run.getResults()) {
                if (r.getThroughputRecordsPerSec() > 0) {
                    katesMetrics.recordFinalThroughput(
                            typeName, r.getThroughputRecordsPerSec(), r.getThroughputMBPerSec());
                }
                if (r.getRecordsSent() > 0) {
                    katesMetrics.recordRecordsProcessed(typeName, r.getRecordsSent());
                }
            }
        }

        repository.save(run);
        return run;
    }

    /**
     * Returns the number of concurrency permits currently in use.
     */
    public int activeTestCount() {
        return maxConcurrentTests - concurrencyGuard.availablePermits();
    }

    /**
     * Returns the configured maximum concurrent test limit.
     */
    public int maxConcurrentTests() {
        return maxConcurrentTests;
    }

    @PreDestroy
    void shutdown() {
        if (activeHandles.isEmpty()) {
            return;
        }
        LOG.infof("Graceful shutdown: stopping %d active test run(s)", activeHandles.size());
        for (var entry : activeHandles.entrySet()) {
            String runId = entry.getKey();
            List<BenchmarkHandle> handles = entry.getValue();
            for (BenchmarkHandle handle : handles) {
                try {
                    String backendName = defaultBackend;
                    var backendResult = resolveBackend(backendName);
                    if (backendResult.isSuccess()) {
                        backendResult.asSuccess().orElseThrow().stop(handle);
                    }
                } catch (Exception e) {
                    LOG.warn("Shutdown: failed to stop task " + handle.taskId(), e);
                }
            }
            try {
                var run = repository.findById(runId);
                if (run.isPresent()) {
                    TestRun updated = run.get().withStatus(TestResult.TaskStatus.FAILED);
                    List<TestResult> newResults = new java.util.ArrayList<>();
                    for (TestResult result : updated.getResults()) {
                        if (result.getStatus() == TestResult.TaskStatus.RUNNING) {
                            result = result.withStatus(TestResult.TaskStatus.FAILED)
                                           .withError("Server shutdown")
                                           .withEndTime(Instant.now().toString());
                        }
                        newResults.add(result);
                    }
                    updated = updated.withResults(newResults);
                    repository.save(updated);
                    LOG.infof("  Shutdown: marked test %s as FAILED", runId);
                }
            } catch (Exception e) {
                LOG.warn("Shutdown: failed to update test run " + runId, e);
            }
        }
        activeHandles.clear();
    }

    public void stopTest(String runId) {
        TestRun run = repository
                .findById(runId)
                .orElseThrow(() -> new IllegalArgumentException("Test run not found: " + runId));

        String backendName = run.getBackend() != null ? run.getBackend() : defaultBackend;
        com.bmscomp.kates.util.Result<BenchmarkBackend, Exception> backendResult = resolveBackend(backendName);
        if (backendResult.isFailure()) {
            return; // Cannot cancel what has no backend.
        }
        BenchmarkBackend backend = backendResult.asSuccess().orElseThrow();

        List<BenchmarkHandle> handles = activeHandles.getOrDefault(runId, List.of());
        for (BenchmarkHandle handle : handles) {
            try {
                backend.stop(handle);
            } catch (Exception e) {
                LOG.warn("Failed to stop task: " + handle.taskId(), e);
            }
        }

        run = run.withStatus(TestResult.TaskStatus.STOPPING);
        repository.save(run);
        fireEvent(run, TestLifecycleEvent.EventKind.STOPPING);
    }

    public List<String> availableBackends() {
        return backends.stream().map(BenchmarkBackend::name).sorted().toList();
    }

    public List<LatencyHeatmapData.HeatmapRow> getHeatmapRows(String runId) {
        return heatmapRows.getOrDefault(runId, List.of());
    }

    /**
     * Merges per-type defaults with the user-supplied spec.
     * User-provided values in the request take priority over type defaults.
     */
    TestSpec applyTypeDefaults(TestType type, TestSpec userSpec) {
        TestTypeDefaults.TypeConfig defaults = typeDefaults.forType(type);
        TestSpec merged = new TestSpec();

        merged.setReplicationFactor(defaults.replicationFactor());
        merged.setPartitions(defaults.partitions());
        merged.setMinInsyncReplicas(defaults.minInsyncReplicas());
        merged.setAcks(defaults.acks());
        merged.setBatchSize(defaults.batchSize());
        merged.setLingerMs(defaults.lingerMs());
        merged.setCompressionType(defaults.compressionType());
        merged.setRecordSize(defaults.recordSize());
        merged.setNumRecords((int) defaults.numRecords());
        merged.setThroughput(defaults.throughput());
        merged.setDurationMs(defaults.durationMs());
        merged.setNumProducers(defaults.numProducers());
        merged.setNumConsumers(defaults.numConsumers());

        if (userSpec != null) {
            if (userSpec.getTopic() != null) merged.setTopic(userSpec.getTopic());
            if (userSpec.hasReplicationFactor()) merged.setReplicationFactor(userSpec.getReplicationFactor());
            if (userSpec.hasPartitions()) merged.setPartitions(userSpec.getPartitions());
            if (userSpec.hasMinInsyncReplicas()) merged.setMinInsyncReplicas(userSpec.getMinInsyncReplicas());
            if (userSpec.hasAcks()) merged.setAcks(userSpec.getAcks());
            if (userSpec.hasBatchSize()) merged.setBatchSize(userSpec.getBatchSize());
            if (userSpec.hasLingerMs()) merged.setLingerMs(userSpec.getLingerMs());
            if (userSpec.hasCompressionType()) merged.setCompressionType(userSpec.getCompressionType());
            if (userSpec.hasRecordSize()) merged.setRecordSize(userSpec.getRecordSize());
            if (userSpec.hasNumRecords()) merged.setNumRecords(userSpec.getNumRecords());
            if (userSpec.hasThroughput()) merged.setThroughput(userSpec.getThroughput());
            if (userSpec.hasDurationMs()) merged.setDurationMs(userSpec.getDurationMs());
            if (userSpec.hasNumProducers()) merged.setNumProducers(userSpec.getNumProducers());
            if (userSpec.hasNumConsumers()) merged.setNumConsumers(userSpec.getNumConsumers());
        }

        return merged;
    }

    private com.bmscomp.kates.util.Result<BenchmarkBackend, Exception> resolveBackend(String name) {
        return backends.stream()
                .filter(b -> b.name().equals(name))
                .findFirst()
                .<com.bmscomp.kates.util.Result<BenchmarkBackend, Exception>>map(com.bmscomp.kates.util.Result::success)
                .orElseGet(() -> com.bmscomp.kates.util.Result.failure(
                        new BenchmarkException("Backend not found: '" + name + "'. Available: " + availableBackends())));
    }

    List<BenchmarkTask> buildTasks(TestType type, TestSpec spec, String runId) {
        String topic = spec.getTopic() != null ? spec.getTopic() : type.name().toLowerCase() + "-test";

        Map<String, String> producerConfig = Map.of(
                "bootstrap.servers", bootstrapServers,
                "acks", spec.getAcks(),
                "batch.size", String.valueOf(spec.getBatchSize()),
                "linger.ms", String.valueOf(spec.getLingerMs()),
                "compression.type", spec.getCompressionType());

        return switch (type) {
            case LOAD ->
                List.of(
                        produceTask(runId + "-produce-0", topic, spec, producerConfig),
                        consumeTask(runId + "-consume-0", topic, spec));
            case STRESS -> {
                var tasks = new java.util.ArrayList<BenchmarkTask>();
                for (int i = 0; i < spec.getNumProducers(); i++) {
                    tasks.add(produceTask(runId + "-stress-" + i, topic, spec, producerConfig));
                }
                yield tasks;
            }
            case SPIKE ->
                List.of(BenchmarkTask.builder(runId + "-spike-burst", BenchmarkTask.WorkloadType.PRODUCE)
                        .topic(topic)
                        .partitions(spec.getPartitions())
                        .targetMessagesPerSec(-1)
                        .maxMessages(spec.getNumRecords())
                        .durationMs(spec.getDurationMs())
                        .recordSize(spec.getRecordSize())
                        .producerConfig(producerConfig)
                        .build());
            case ENDURANCE ->
                List.of(
                        produceTask(runId + "-endurance-produce", topic, spec, producerConfig),
                        consumeTask(runId + "-endurance-consume", topic, spec));
            case VOLUME -> List.of(produceTask(runId + "-volume-0", topic, spec, producerConfig));
            case CAPACITY -> {
                var tasks = new java.util.ArrayList<BenchmarkTask>();
                for (int i = 0; i < spec.getNumProducers(); i++) {
                    tasks.add(BenchmarkTask.builder(runId + "-cap-" + i, BenchmarkTask.WorkloadType.PRODUCE)
                            .topic(topic)
                            .partitions(spec.getPartitions())
                            .targetMessagesPerSec(-1)
                            .maxMessages(spec.getNumRecords())
                            .durationMs(spec.getDurationMs())
                            .recordSize(spec.getRecordSize())
                            .producerConfig(producerConfig)
                            .build());
                }
                yield tasks;
            }
            case ROUND_TRIP ->
                List.of(BenchmarkTask.builder(runId + "-roundtrip-0", BenchmarkTask.WorkloadType.ROUND_TRIP)
                        .topic(topic)
                        .partitions(spec.getPartitions())
                        .targetMessagesPerSec(spec.getThroughput())
                        .maxMessages(spec.getNumRecords())
                        .durationMs(spec.getDurationMs())
                        .recordSize(spec.getRecordSize())
                        .producerConfig(producerConfig)
                        .build());
            case INTEGRITY ->
                List.of(BenchmarkTask.builder(runId + "-integrity-0", BenchmarkTask.WorkloadType.INTEGRITY)
                        .topic(topic)
                        .partitions(spec.getPartitions())
                        .targetMessagesPerSec(spec.getThroughput())
                        .maxMessages(spec.getNumRecords())
                        .durationMs(spec.getDurationMs())
                        .recordSize(spec.getRecordSize())
                        .consumerGroup(spec.getConsumerGroup() != null ? spec.getConsumerGroup() : "integrity-cg")
                        .producerConfig(producerConfig)
                        .enableIdempotence(spec.isEnableIdempotence())
                        .enableTransactions(spec.isEnableTransactions())
                        .enableCrc(spec.isEnableCrc())
                        .build());
            case TUNE_REPLICATION, TUNE_ACKS, TUNE_BATCHING, TUNE_COMPRESSION, TUNE_PARTITIONS ->
                List.of(produceTask(runId + "-tune-0", topic, spec, producerConfig));
        };
    }

    private List<BenchmarkTask> buildPhaseTask(
            ScenarioPhase phase, TestSpec spec, TestType type, String runId, String phaseName) {
        String topic = spec.getTopic() != null ? spec.getTopic() : type.name().toLowerCase() + "-test";
        Map<String, String> producerConfig = new HashMap<>();
        producerConfig.put("acks", spec.getAcks());
        producerConfig.put("batch.size", String.valueOf(spec.getBatchSize()));
        producerConfig.put("linger.ms", String.valueOf(spec.getLingerMs()));
        producerConfig.put("compression.type", spec.getCompressionType());

        String taskId = runId + "-" + phaseName;

        return switch (phase.getPhaseType()) {
            case WARMUP, STEADY, COOLDOWN -> List.of(produceTask(taskId + "-produce", topic, spec, producerConfig));
            case RAMP -> {
                var tasks = new java.util.ArrayList<BenchmarkTask>();
                int steps = Math.max(1, phase.getRampSteps());
                int baseTarget = Math.max(1, spec.getThroughput() / steps);
                for (int s = 0; s < steps; s++) {
                    int stepTarget = baseTarget * (s + 1);
                    TestSpec stepSpec = new TestSpec();
                    stepSpec.setTopic(topic);
                    stepSpec.setPartitions(spec.getPartitions());
                    stepSpec.setThroughput(stepTarget);
                    stepSpec.setNumRecords(spec.getNumRecords() / steps);
                    stepSpec.setDurationMs(spec.getDurationMs() / steps);
                    stepSpec.setRecordSize(spec.getRecordSize());
                    tasks.add(produceTask(taskId + "-ramp-" + s, topic, stepSpec, producerConfig));
                }
                yield tasks;
            }
            case SPIKE ->
                List.of(BenchmarkTask.builder(taskId + "-spike", BenchmarkTask.WorkloadType.PRODUCE)
                        .topic(topic)
                        .partitions(spec.getPartitions())
                        .targetMessagesPerSec(-1)
                        .maxMessages(spec.getNumRecords())
                        .durationMs(spec.getDurationMs())
                        .recordSize(spec.getRecordSize())
                        .producerConfig(producerConfig)
                        .build());
        };
    }

    private BenchmarkTask produceTask(String taskId, String topic, TestSpec spec, Map<String, String> producerConfig) {
        return BenchmarkTask.builder(taskId, BenchmarkTask.WorkloadType.PRODUCE)
                .topic(topic)
                .partitions(spec.getPartitions())
                .targetMessagesPerSec(spec.getThroughput())
                .maxMessages(spec.getNumRecords())
                .durationMs(spec.getDurationMs())
                .recordSize(spec.getRecordSize())
                .producerConfig(producerConfig)
                .build();
    }

    private BenchmarkTask consumeTask(String taskId, String topic, TestSpec spec) {
        return BenchmarkTask.builder(taskId, BenchmarkTask.WorkloadType.CONSUME)
                .topic(topic)
                .partitions(spec.getPartitions())
                .maxMessages(spec.getNumRecords())
                .durationMs(spec.getDurationMs())
                .consumerGroup(taskId + "-group")
                .build();
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
    }

    private TestResult applyStatus(TestResult result, BenchmarkStatus status) {
        result = result.withStatus(status.getState())
                .withRecordsSent(status.getRecordsProcessed())
                .withThroughputRecordsPerSec(status.getThroughputRecordsPerSec())
                .withThroughputMBPerSec(status.getThroughputMBPerSec())
                .withAvgLatencyMs(status.getAvgLatencyMs())
                .withP50LatencyMs(status.getP50LatencyMs())
                .withP95LatencyMs(status.getP95LatencyMs())
                .withP99LatencyMs(status.getP99LatencyMs())
                .withMaxLatencyMs(status.getMaxLatencyMs());

        if (status.getError() != null) {
            result = result.withError(status.getError());
        }
        if (status.getIntegrityResult() != null) {
            result = result.withIntegrity(status.getIntegrityResult());
        }
        if (status.isTerminal()) {
            result = result.withEndTime(Instant.now().toString());
        }
        return result;
    }

    private void fireEvent(TestRun run, TestLifecycleEvent.EventKind kind) {
        String type = run.getTestType() != null ? run.getTestType().name() : "UNKNOWN";
        lifecycleEvents.fireAsync(new TestLifecycleEvent(run.getId(), type, kind));
    }
}
