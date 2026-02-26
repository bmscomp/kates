package com.klster.kates.engine;

import java.time.Instant;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Any;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;

import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import com.klster.kates.config.TestTypeDefaults;
import com.klster.kates.domain.CreateTestRequest;
import com.klster.kates.domain.ScenarioPhase;
import com.klster.kates.domain.TestResult;
import com.klster.kates.domain.TestRun;
import com.klster.kates.domain.TestScenario;
import com.klster.kates.domain.TestSpec;
import com.klster.kates.domain.TestType;
import com.klster.kates.export.LatencyHeatmapData;
import com.klster.kates.service.KafkaAdminService;
import com.klster.kates.service.TestRunRepository;
import com.klster.kates.webhook.WebhookService;

/**
 * Orchestrator that routes benchmark execution to pluggable backends.
 * Applies per-test-type defaults from configuration before building tasks.
 */
@ApplicationScoped
public class TestOrchestrator {

    private static final Logger LOG = Logger.getLogger(TestOrchestrator.class);

    private final KafkaAdminService kafkaAdmin;
    private final TestRunRepository repository;
    private final Instance<BenchmarkBackend> backends;
    private final TestTypeDefaults typeDefaults;
    private final BenchmarkMetrics benchmarkMetrics;
    private final KatesMetrics katesMetrics;
    private final WebhookService webhookService;
    private final String defaultBackend;
    private final String bootstrapServers;
    private final Map<String, List<BenchmarkHandle>> activeHandles = new ConcurrentHashMap<>();
    private final Map<String, List<LatencyHeatmapData.HeatmapRow>> heatmapRows = new ConcurrentHashMap<>();

    @Inject
    public TestOrchestrator(
            KafkaAdminService kafkaAdmin,
            TestRunRepository repository,
            @Any Instance<BenchmarkBackend> backends,
            TestTypeDefaults typeDefaults,
            BenchmarkMetrics benchmarkMetrics,
            KatesMetrics katesMetrics,
            WebhookService webhookService,
            @ConfigProperty(name = "kates.engine.default-backend", defaultValue = "native") String defaultBackend,
            @ConfigProperty(name = "kates.kafka.bootstrap-servers") String bootstrapServers) {
        this.kafkaAdmin = kafkaAdmin;
        this.repository = repository;
        this.backends = backends;
        this.typeDefaults = typeDefaults;
        this.benchmarkMetrics = benchmarkMetrics;
        this.katesMetrics = katesMetrics;
        this.webhookService = webhookService;
        this.defaultBackend = defaultBackend;
        this.bootstrapServers = bootstrapServers;
    }

    @PostConstruct
    void recoverOrphans() {
        List<TestRun> orphans = repository.findByStatus(TestResult.TaskStatus.RUNNING);
        if (orphans.isEmpty()) {
            return;
        }
        LOG.infof("Recovering %d orphaned RUNNING tests from previous lifecycle", orphans.size());
        for (TestRun run : orphans) {
            run.setStatus(TestResult.TaskStatus.FAILED);
            for (TestResult result : run.getResults()) {
                if (result.getStatus() == TestResult.TaskStatus.RUNNING) {
                    result.setStatus(TestResult.TaskStatus.FAILED);
                    result.setError("Recovered: test was orphaned after server restart");
                }
            }
            repository.save(run);
            LOG.infof("  Marked test %s as FAILED (orphan recovery)", run.getId());
        }
    }

    public TestRun executeTest(CreateTestRequest request) {
        if (request.isScenario()) {
            return executeScenario(request);
        }

        TestType type = request.getType();
        TestSpec spec = applyTypeDefaults(type, request.getSpec());
        String backendName = request.getBackend() != null ? request.getBackend() : defaultBackend;

        BenchmarkBackend backend = resolveBackend(backendName);

        TestRun run = new TestRun(type, spec);
        run.setBackend(backendName);
        repository.save(run);

        try {
            createTestTopic(spec, type);
            List<BenchmarkTask> tasks = buildTasks(type, spec, run.getId());
            run.setStatus(TestResult.TaskStatus.RUNNING);
            benchmarkMetrics.startRun(run.getId(), type.name(), backendName);

            var handles = new java.util.ArrayList<BenchmarkHandle>();

            for (BenchmarkTask task : tasks) {
                try {
                    BenchmarkHandle handle = backend.submit(task);
                    handles.add(handle);

                    TestResult result = new TestResult();
                    result.setTaskId(task.getTaskId());
                    result.setTestType(type);
                    result.setStatus(TestResult.TaskStatus.RUNNING);
                    result.setStartTime(Instant.now().toString());
                    run.addResult(result);
                    LOG.info("Submitted task via " + backendName + ": " + task.getTaskId());
                } catch (Exception e) {
                    LOG.warn("Failed to submit task: " + task.getTaskId(), e);
                    TestResult failedResult = new TestResult();
                    failedResult.setTaskId(task.getTaskId());
                    failedResult.setTestType(type);
                    failedResult.setStatus(TestResult.TaskStatus.FAILED);
                    failedResult.setError(e.getMessage());
                    failedResult.setStartTime(Instant.now().toString());
                    failedResult.setEndTime(Instant.now().toString());
                    run.addResult(failedResult);
                }
            }

            activeHandles.put(run.getId(), handles);

            boolean allFailed = run.getResults().stream().allMatch(r -> r.getStatus() == TestResult.TaskStatus.FAILED);
            if (allFailed) {
                run.setStatus(TestResult.TaskStatus.FAILED);
            }

        } catch (Exception e) {
            LOG.error("Test execution failed for run: " + run.getId(), e);
            run.setStatus(TestResult.TaskStatus.FAILED);
        }

        repository.save(run);
        if (run.getStatus() == TestResult.TaskStatus.FAILED || run.getStatus() == TestResult.TaskStatus.DONE) {
            benchmarkMetrics.endRun(run.getId());
        }
        return run;
    }

    /**
     * Executes a multi-phase scenario: each phase runs sequentially,
     * using the resolved spec (base + phase overrides + type defaults).
     */
    TestRun executeScenario(CreateTestRequest request) {
        TestScenario scenario = request.getScenario();
        TestType type = scenario.getType() != null ? scenario.getType() : request.getType();
        String backendName = scenario.getBackend() != null
                ? scenario.getBackend()
                : (request.getBackend() != null ? request.getBackend() : defaultBackend);

        BenchmarkBackend backend = resolveBackend(backendName);

        TestSpec baseSpec = applyTypeDefaults(type, scenario.getBaseSpec());
        TestRun run = new TestRun(type, baseSpec);
        run.setBackend(backendName);
        run.setScenarioName(scenario.getName());
        run.setLabels(scenario.getLabels());
        run.setSla(scenario.getSla());
        run.setStatus(TestResult.TaskStatus.RUNNING);
        repository.save(run);

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

                        TestResult result = new TestResult();
                        result.setTaskId(task.getTaskId());
                        result.setTestType(type);
                        result.setStatus(TestResult.TaskStatus.RUNNING);
                        result.setStartTime(Instant.now().toString());
                        result.setPhaseName(phaseName);
                        run.addResult(result);
                        LOG.info("Scenario phase [" + phaseName + "] submitted: " + task.getTaskId());
                    } catch (Exception e) {
                        LOG.warn("Phase [" + phaseName + "] failed to submit: " + task.getTaskId(), e);
                        TestResult failedResult = new TestResult();
                        failedResult.setTaskId(task.getTaskId());
                        failedResult.setTestType(type);
                        failedResult.setStatus(TestResult.TaskStatus.FAILED);
                        failedResult.setError(e.getMessage());
                        failedResult.setStartTime(Instant.now().toString());
                        failedResult.setEndTime(Instant.now().toString());
                        failedResult.setPhaseName(phaseName);
                        run.addResult(failedResult);
                    }
                }
            }

            activeHandles.put(run.getId(), allHandles);

            boolean allFailed = run.getResults().stream().allMatch(r -> r.getStatus() == TestResult.TaskStatus.FAILED);
            if (allFailed) {
                run.setStatus(TestResult.TaskStatus.FAILED);
            }

        } catch (Exception e) {
            LOG.error("Scenario execution failed for run: " + run.getId(), e);
            run.setStatus(TestResult.TaskStatus.FAILED);
        }

        repository.save(run);
        if (run.getStatus() == TestResult.TaskStatus.FAILED || run.getStatus() == TestResult.TaskStatus.DONE) {
            benchmarkMetrics.endRun(run.getId());
        }
        return run;
    }

    public TestRun refreshStatus(String runId) {
        TestRun run = repository
                .findById(runId)
                .orElseThrow(() -> new IllegalArgumentException("Test run not found: " + runId));

        String backendName = run.getBackend() != null ? run.getBackend() : defaultBackend;
        BenchmarkBackend backend = resolveBackend(backendName);

        List<BenchmarkHandle> handles = activeHandles.getOrDefault(runId, List.of());
        Map<String, BenchmarkHandle> handleMap = new HashMap<>();
        for (BenchmarkHandle h : handles) {
            handleMap.put(h.taskId(), h);
        }

        boolean allDone = true;
        boolean anyFailed = false;

        for (TestResult result : run.getResults()) {
            if (result.getStatus() == TestResult.TaskStatus.RUNNING
                    || result.getStatus() == TestResult.TaskStatus.PENDING) {
                BenchmarkHandle handle = handleMap.get(result.getTaskId());
                if (handle != null) {
                    try {
                        BenchmarkStatus status = backend.poll(handle);
                        applyStatus(result, status);

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
        }

        if (allDone) {
            run.setStatus(anyFailed ? TestResult.TaskStatus.FAILED : TestResult.TaskStatus.DONE);
            activeHandles.remove(runId);

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

    public void stopTest(String runId) {
        TestRun run = repository
                .findById(runId)
                .orElseThrow(() -> new IllegalArgumentException("Test run not found: " + runId));

        String backendName = run.getBackend() != null ? run.getBackend() : defaultBackend;
        BenchmarkBackend backend = resolveBackend(backendName);

        List<BenchmarkHandle> handles = activeHandles.getOrDefault(runId, List.of());
        for (BenchmarkHandle handle : handles) {
            try {
                backend.stop(handle);
            } catch (Exception e) {
                LOG.warn("Failed to stop task: " + handle.taskId(), e);
            }
        }

        run.setStatus(TestResult.TaskStatus.STOPPING);
        repository.save(run);
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
            if (userSpec.getTopic() != null)
                merged.setTopic(userSpec.getTopic());
            if (userSpec.getReplicationFactor() != 3)
                merged.setReplicationFactor(userSpec.getReplicationFactor());
            if (userSpec.getPartitions() != 3)
                merged.setPartitions(userSpec.getPartitions());
            if (userSpec.getMinInsyncReplicas() != 2)
                merged.setMinInsyncReplicas(userSpec.getMinInsyncReplicas());
            if (!"all".equals(userSpec.getAcks()))
                merged.setAcks(userSpec.getAcks());
            if (userSpec.getBatchSize() != 65536)
                merged.setBatchSize(userSpec.getBatchSize());
            if (userSpec.getLingerMs() != 5)
                merged.setLingerMs(userSpec.getLingerMs());
            if (!"lz4".equals(userSpec.getCompressionType()))
                merged.setCompressionType(userSpec.getCompressionType());
            if (userSpec.getRecordSize() != 1024)
                merged.setRecordSize(userSpec.getRecordSize());
            if (userSpec.getNumRecords() != 1_000_000)
                merged.setNumRecords(userSpec.getNumRecords());
            if (userSpec.getThroughput() != -1)
                merged.setThroughput(userSpec.getThroughput());
            if (userSpec.getDurationMs() != 600_000)
                merged.setDurationMs(userSpec.getDurationMs());
            if (userSpec.getNumProducers() != 1)
                merged.setNumProducers(userSpec.getNumProducers());
            if (userSpec.getNumConsumers() != 1)
                merged.setNumConsumers(userSpec.getNumConsumers());
        }

        return merged;
    }

    private BenchmarkBackend resolveBackend(String name) {
        return backends.stream()
                .filter(b -> b.name().equals(name))
                .findFirst()
                .orElseThrow(() -> new BenchmarkException(
                        "Backend not found: '" + name + "'. Available: " + availableBackends()));
    }

    private List<BenchmarkTask> buildTasks(TestType type, TestSpec spec, String runId) {
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
            case TUNE_REPLICATION, TUNE_ACKS, TUNE_BATCHING,
                 TUNE_COMPRESSION, TUNE_PARTITIONS ->
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
        String topicName = spec.getTopic() != null ? spec.getTopic() : type.name().toLowerCase() + "-test";
        Map<String, String> topicConfig = new HashMap<>();
        topicConfig.put("min.insync.replicas", String.valueOf(spec.getMinInsyncReplicas()));

        if (type == TestType.VOLUME) {
            topicConfig.put("retention.ms", "1800000");
            topicConfig.put("max.message.bytes", "1048576");
        }

        kafkaAdmin.createTopic(topicName, spec.getPartitions(), spec.getReplicationFactor(), topicConfig);
    }

    private void applyStatus(TestResult result, BenchmarkStatus status) {
        result.setStatus(status.getState());
        result.setRecordsSent(status.getRecordsProcessed());
        result.setThroughputRecordsPerSec(status.getThroughputRecordsPerSec());
        result.setThroughputMBPerSec(status.getThroughputMBPerSec());
        result.setAvgLatencyMs(status.getAvgLatencyMs());
        result.setP50LatencyMs(status.getP50LatencyMs());
        result.setP95LatencyMs(status.getP95LatencyMs());
        result.setP99LatencyMs(status.getP99LatencyMs());
        result.setMaxLatencyMs(status.getMaxLatencyMs());

        if (status.getError() != null) {
            result.setError(status.getError());
        }
        if (status.getIntegrityResult() != null) {
            result.setIntegrity(status.getIntegrityResult());
        }
        if (status.isTerminal()) {
            result.setEndTime(Instant.now().toString());
        }
    }
}
