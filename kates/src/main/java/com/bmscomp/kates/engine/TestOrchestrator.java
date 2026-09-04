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

import io.quarkus.scheduler.Scheduled;
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
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.service.TopicService;

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
    private final Event<TestLifecycleEvent> lifecycleEvents;
    private final String defaultBackend;
    private final String bootstrapServers;
    private final int maxConcurrentTests;
    private final Semaphore concurrencyGuard;
    private final Map<String, List<BenchmarkHandle>> activeHandles = new ConcurrentHashMap<>();

    /**
     * Runs currently holding a concurrency permit. The permit is held for the
     * run's whole LIFETIME, not just its submission — releasing it when
     * {@code executeAsync} returned made the cap meaningless, because submission
     * completes in milliseconds while the workers it starts run for minutes.
     * Membership here also makes release idempotent: the terminal transition,
     * the reaper and the failure paths can all call
     * {@link #releasePermit(String)} without over-releasing the semaphore.
     */
    private final java.util.Set<String> permitHolders = ConcurrentHashMap.newKeySet();
    /**
     * Heatmap rows are read by ReportResource after a run completes, so they
     * cannot be dropped on completion — instead retain the most recent
     * {@link #MAX_HEATMAP_RUNS} runs (previously unbounded: grew forever).
     */
    private static final int MAX_HEATMAP_RUNS = 50;

    private final Map<String, List<LatencyHeatmapData.HeatmapRow>> heatmapRows = new ConcurrentHashMap<>();
    private final java.util.concurrent.ConcurrentLinkedDeque<String> heatmapOrder =
            new java.util.concurrent.ConcurrentLinkedDeque<>();
    private final Map<String, Long> runStartNanos = new ConcurrentHashMap<>();

    @Inject
    public TestOrchestrator(
            TopicService topicService,
            TestRunRepository repository,
            @Any Instance<BenchmarkBackend> backends,
            TestTypeDefaults typeDefaults,
            BenchmarkMetrics benchmarkMetrics,
            KatesMetrics katesMetrics,
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
            return com.bmscomp.kates.util.Result.failure(new ConcurrencyLimitException(maxConcurrentTests));
        }

        TestType type = request.getType();
        TestSpec spec = applyTypeDefaults(type, request.getSpec());
        String backendName = request.getBackend() != null ? request.getBackend() : defaultBackend;

        com.bmscomp.kates.util.Result<BenchmarkBackend, Exception> backendResult = resolveBackend(backendName);
        if (backendResult.isFailure()) {
            concurrencyGuard.release();
            return com.bmscomp.kates.util.Result.failure(
                    backendResult.asFailure().orElseThrow());
        }
        BenchmarkBackend backend = backendResult.asSuccess().orElseThrow();

        TestRun run = new TestRun(type, spec).withBackend(backendName);
        // Register BEFORE the first thing that can throw. A transient failure in
        // save/fireEvent used to strand the permit forever (the semaphore drained
        // one permit per failure until restart), because nothing had recorded
        // this run as a holder yet.
        permitHolders.add(run.getId());
        try {
            repository.save(run);
            fireEvent(run, TestLifecycleEvent.EventKind.CREATED);
        } catch (RuntimeException e) {
            releasePermit(run.getId());
            LOG.error("Failed to register test run: " + run.getId(), e);
            markStrandedAsFailed(run);
            return com.bmscomp.kates.util.Result.failure(e);
        }

        Thread.startVirtualThread(() -> {
            try {
                executeAsync(run, type, spec, backendName, backend);
                // NOTE: no release here. executeAsync only SUBMITS work; the
                // permit is released on the terminal transition (refreshStatus /
                // the reconciler), by the reaper, or by executeAsync itself when
                // the run ends terminal at submission time.
            } catch (Throwable t) {
                LOG.error("Test submission failed for run: " + run.getId(), t);
                releasePermit(run.getId());
            }
        });

        return com.bmscomp.kates.util.Result.success(run);
    }

    @io.opentelemetry.instrumentation.annotations.WithSpan("TestOrchestrator.executeAsync")
    void executeAsync(TestRun run, TestType type, TestSpec spec, String backendName, BenchmarkBackend backend) {
        org.jboss.logging.MDC.put("runId", run.getId());
        org.jboss.logging.MDC.put("testType", type.name());
        org.jboss.logging.MDC.put("backend", backendName);
        runStartNanos.put(run.getId(), System.nanoTime());
        List<BenchmarkHandle> submitted = List.of();
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
                            .withPhaseName(phaseNameFor(task))
                            .withStatus(TestResult.TaskStatus.RUNNING)
                            .withStartTime(Instant.now().toString());
                    run = run.withAddedResult(result);
                    LOG.info("Submitted task via " + backendName + ": " + task.getTaskId());
                } catch (Exception e) {
                    LOG.warn("Failed to submit task: " + task.getTaskId(), e);
                    TestResult failedResult = new TestResult()
                            .withTaskId(task.getTaskId())
                            .withTestType(type)
                            .withPhaseName(phaseNameFor(task))
                            .withStatus(TestResult.TaskStatus.FAILED)
                            .withError(e.getMessage())
                            .withStartTime(Instant.now().toString())
                            .withEndTime(Instant.now().toString());
                    run = run.withAddedResult(failedResult);
                }
            }

            submitted = handles;

            boolean allFailed = run.getResults().stream().allMatch(r -> r.getStatus() == TestResult.TaskStatus.FAILED);
            if (allFailed) {
                run = run.withStatus(TestResult.TaskStatus.FAILED);
            }

        } catch (Exception e) {
            LOG.error("Test execution failed for run: " + run.getId(), e);
            run = run.withStatus(TestResult.TaskStatus.FAILED);
        }

        // Registered only AFTER the row carrying these tasks is persisted.
        // Publishing the handles first let the 5s reconciler read the run and
        // write its own version of the row while this method was still building
        // it — a lost update, or an optimistic-lock failure thrown into the
        // virtual thread. The finally matters: workers are already running by
        // now, so a failed save must not leave them with no handle to stop them.
        try {
            repository.save(run);
        } finally {
            registerHandles(run, submitted);
        }
        if (run.getStatus() == TestResult.TaskStatus.FAILED) {
            fireEvent(run, TestLifecycleEvent.EventKind.FAILED);
            benchmarkMetrics.endRun(run.getId());
            releasePermit(run.getId());
        } else if (run.getStatus() == TestResult.TaskStatus.DONE) {
            fireEvent(run, TestLifecycleEvent.EventKind.DONE);
            benchmarkMetrics.endRun(run.getId());
            releasePermit(run.getId());
        }
        org.jboss.logging.MDC.remove("runId");
        org.jboss.logging.MDC.remove("testType");
        org.jboss.logging.MDC.remove("backend");
    }

    /**
     * Executes a multi-phase scenario, using the resolved spec per phase
     * (base + phase overrides + type defaults).
     *
     * <p>Phases are SUBMITTED in order, not run one after another: the loop
     * below hands every phase's tasks to the backend without waiting for the
     * previous phase to finish, so they overlap. The javadoc here used to claim
     * they ran sequentially, which is worth correcting because it changes how
     * you read a scenario's results — a ramp defined as three phases produces
     * three concurrent loads, not a staircase.
     */
    @io.opentelemetry.instrumentation.annotations.WithSpan("TestOrchestrator.executeScenario")
    com.bmscomp.kates.util.Result<TestRun, Exception> executeScenario(CreateTestRequest request) {
        TestScenario scenario = request.getScenario();
        TestType type = scenario.getType() != null ? scenario.getType() : request.getType();
        String backendName = scenario.getBackend() != null
                ? scenario.getBackend()
                : (request.getBackend() != null ? request.getBackend() : defaultBackend);

        // Scenarios previously bypassed the concurrency cap entirely — executeTest
        // delegates here BEFORE its tryAcquire, so any number of multi-phase runs
        // could start at once. They consume the same brokers as plain runs, so
        // they take a permit on the same terms.
        if (!concurrencyGuard.tryAcquire()) {
            return com.bmscomp.kates.util.Result.failure(new ConcurrencyLimitException(maxConcurrentTests));
        }

        com.bmscomp.kates.util.Result<BenchmarkBackend, Exception> backendResult = resolveBackend(backendName);
        if (backendResult.isFailure()) {
            concurrencyGuard.release();
            return com.bmscomp.kates.util.Result.failure(
                    backendResult.asFailure().orElseThrow());
        }
        BenchmarkBackend backend = backendResult.asSuccess().orElseThrow();

        TestSpec baseSpec = applyTypeDefaults(type, scenario.getBaseSpec());
        TestRun run = new TestRun(type, baseSpec)
                .withBackend(backendName)
                .withScenarioName(scenario.getName())
                .withLabels(scenario.getLabels())
                .withSla(scenario.getSla())
                .withStatus(TestResult.TaskStatus.RUNNING);
        // Same ordering rule as executeTest: hold the permit before anything
        // that can throw, so a failed save cannot strand it.
        permitHolders.add(run.getId());
        try {
            repository.save(run);
            fireEvent(run, TestLifecycleEvent.EventKind.CREATED);
            fireEvent(run, TestLifecycleEvent.EventKind.RUNNING);
        } catch (RuntimeException e) {
            releasePermit(run.getId());
            LOG.error("Failed to register scenario run: " + run.getId(), e);
            markStrandedAsFailed(run);
            return com.bmscomp.kates.util.Result.failure(e);
        }
        runStartNanos.put(run.getId(), System.nanoTime());
        List<BenchmarkHandle> submitted = List.of();

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

            submitted = allHandles;

            boolean allFailed = run.getResults().stream().allMatch(r -> r.getStatus() == TestResult.TaskStatus.FAILED);
            if (allFailed) {
                run = run.withStatus(TestResult.TaskStatus.FAILED);
            }

        } catch (Exception e) {
            LOG.error("Scenario execution failed for run: " + run.getId(), e);
            run = run.withStatus(TestResult.TaskStatus.FAILED);
        }

        // Same ordering rule as executeAsync: publish handles only once the row
        // they belong to is persisted, so the reconciler cannot race this write
        // — but publish them even if that write fails, so running workers stay
        // stoppable.
        try {
            repository.save(run);
        } finally {
            registerHandles(run, submitted);
        }
        if (run.getStatus() == TestResult.TaskStatus.FAILED) {
            fireEvent(run, TestLifecycleEvent.EventKind.FAILED);
            benchmarkMetrics.endRun(run.getId());
            releasePermit(run.getId());
        } else if (run.getStatus() == TestResult.TaskStatus.DONE) {
            fireEvent(run, TestLifecycleEvent.EventKind.DONE);
            benchmarkMetrics.endRun(run.getId());
            releasePermit(run.getId());
        }
        return com.bmscomp.kates.util.Result.success(run);
    }

    public TestRun refreshStatus(String runId) {
        TestRun run = repository
                .findById(runId)
                .orElseThrow(() -> new IllegalArgumentException("Test run not found: " + runId));

        // Snapshot the status BEFORE this poll so the terminal transition (and
        // its one-shot events + metrics) fires exactly once. Without this,
        // polling a run that is already DONE/FAILED — which the scheduled
        // reconciler and repeat client polls both do — would re-fire the
        // lifecycle event and double-count completion metrics.
        TestResult.TaskStatus priorStatus = run.getStatus();
        if (priorStatus == TestResult.TaskStatus.DONE || priorStatus == TestResult.TaskStatus.FAILED) {
            activeHandles.remove(runId);
            return run;
        }

        // What the run looks like before this poll. The reconciler calls this
        // every 5s for every active run, and a poll that finds nothing new used
        // to write the row anyway — an UPDATE and a version bump per run per
        // tick, which also collides with concurrent writers for no reason.
        String signatureBefore = pollSignature(run);

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

                        // Propagate CDC phase data to the TestRun
                        if (status.getPhaseDurations() != null
                                && !status.getPhaseDurations().isEmpty()) {
                            run = run.withCdcPhases(status.getPhaseDurations());
                        }
                        if (status.getCurrentPhase() != null) {
                            run = run.withCdcPhase(status.getCurrentPhase());
                        }

                        if (status.getHeatmapBuckets() != null) {
                            heatmapRows
                                    .computeIfAbsent(runId, k -> {
                                        heatmapOrder.addLast(k);
                                        return java.util.Collections.synchronizedList(new java.util.ArrayList<>());
                                    })
                                    .add(new LatencyHeatmapData.HeatmapRow(
                                            System.currentTimeMillis(),
                                            result.getPhaseName(),
                                            status.getHeatmapBuckets()));
                            while (heatmapRows.size() > MAX_HEATMAP_RUNS) {
                                String eldest = heatmapOrder.pollFirst();
                                if (eldest == null) {
                                    break;
                                }
                                if (eldest.equals(runId)) {
                                    heatmapOrder.addLast(eldest);
                                    break;
                                }
                                heatmapRows.remove(eldest);
                            }
                            // An id evicted above can be re-added by a later
                            // poll of the same run, leaving its earlier entry
                            // in the deque. Those stale duplicates are never
                            // removed by the loop (the map no longer has them),
                            // so the deque grows even though the map does not.
                            heatmapOrder.removeIf(id -> !heatmapRows.containsKey(id));
                        }
                    } catch (Exception e) {
                        LOG.warn("Failed to poll task: " + result.getTaskId(), e);
                    }
                }
            }

            updatedResults.add(result);
        }

        updatedResults = abortStrandedConsumers(updatedResults, backend, handleMap);

        for (TestResult result : updatedResults) {
            if (result.getStatus() != TestResult.TaskStatus.DONE
                    && result.getStatus() != TestResult.TaskStatus.FAILED) {
                allDone = false;
            }
            if (result.getStatus() == TestResult.TaskStatus.FAILED) {
                anyFailed = true;
            }
        }
        run = run.withResults(updatedResults);

        if (allDone && !updatedResults.isEmpty()) {
            run = run.withStatus(anyFailed ? TestResult.TaskStatus.FAILED : TestResult.TaskStatus.DONE);
            activeHandles.remove(runId);
            fireEvent(run, anyFailed ? TestLifecycleEvent.EventKind.FAILED : TestLifecycleEvent.EventKind.DONE);

            String typeName = run.getTestType() != null ? run.getTestType().name() : "UNKNOWN";
            String outcome = anyFailed ? "failed" : "done";
            katesMetrics.recordTestCompleted(typeName, outcome);

            Long startNanos = runStartNanos.remove(runId);
            if (startNanos != null) {
                long durationNanos = System.nanoTime() - startNanos;
                katesMetrics.recordTestDuration(typeName, java.time.Duration.ofNanos(durationNanos));
            } else if (run.getCreatedAt() != null) {
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
                    // Feed the per-run gauges before they are unregistered. They
                    // were registered by startRun but never written to, so every
                    // kates.benchmark.throughput.* series read a constant 0.
                    benchmarkMetrics.recordThroughput(
                            runId, r.getPhaseName(), r.getThroughputRecordsPerSec(), r.getThroughputMBPerSec());
                }
                if (r.getRecordsSent() > 0) {
                    katesMetrics.recordRecordsProcessed(typeName, r.getRecordsSent());
                }
                if (r.getStatus() == TestResult.TaskStatus.FAILED) {
                    benchmarkMetrics.recordError(runId, r.getPhaseName());
                }
            }

            // Unregister the run's meters and hand back its concurrency slot.
            // Both are keyed on the run and both leaked before: meters accumulated
            // in the registry forever, and the permit had already been released at
            // submission time so the cap never applied.
            benchmarkMetrics.endRun(runId);
            releasePermit(runId);
        }

        if (!pollSignature(run).equals(signatureBefore)) {
            try {
                repository.save(run);
            } catch (jakarta.persistence.OptimisticLockException e) {
                // Another writer (the reconciler, the reaper, a concurrent GET)
                // moved the row first. Their write is as valid as this one, so
                // return what is actually stored rather than turning a plain
                // read into a 409 for the client.
                LOG.debugf("Lost the optimistic-lock race refreshing %s; returning the stored run", runId);
                return repository.findById(runId).orElse(run);
            }
        }
        return run;
    }

    /**
     * Best-effort FAILED for a run whose registration blew up half-way.
     *
     * <p>If the save succeeded and only the event failed, the row is left
     * PENDING — a state nothing scans: orphan recovery looks for RUNNING and the
     * timeout reaper only reaps RUNNING, so the run would sit there forever
     * looking like it was about to start. Failing to write this is not worth
     * masking the original error, so it only logs.
     */
    private void markStrandedAsFailed(TestRun run) {
        try {
            repository.save(run.withStatus(TestResult.TaskStatus.FAILED));
        } catch (RuntimeException e) {
            LOG.warnf("Could not mark stranded run %s as FAILED: %s", run.getId(), e.getMessage());
        }
    }

    /**
     * Publishes a run's backend handles so the reconciler, the reaper and
     * shutdown can poll and stop its workers.
     *
     * <p>Registered even when the run already looks terminal: a partial
     * submission can leave workers running behind a FAILED status, and without a
     * handle nothing can stop them. The terminal path in
     * {@link #refreshStatus(String)} drops the entry once the run is settled.
     */
    private void registerHandles(TestRun run, List<BenchmarkHandle> handles) {
        if (!handles.isEmpty()) {
            activeHandles.put(run.getId(), handles);
        }
    }

    /**
     * Everything a poll can change about a run, as a comparable string. Used to
     * skip writes when a reconcile tick found nothing new.
     */
    private static String pollSignature(TestRun run) {
        StringBuilder sb = new StringBuilder(64);
        sb.append(run.getStatus()).append('|').append(run.getCdcPhase());
        for (TestResult r : run.getResults()) {
            sb.append('#')
                    .append(r.getTaskId())
                    .append(':')
                    .append(r.getStatus())
                    .append(':')
                    .append(r.getRecordsSent())
                    .append(':')
                    .append(r.getThroughputRecordsPerSec())
                    .append(':')
                    .append(r.getAvgLatencyMs())
                    .append(':')
                    .append(r.getP99LatencyMs())
                    .append(':')
                    .append(r.getEndTime())
                    .append(':')
                    .append(r.getError());
        }
        return sb.toString();
    }

    /**
     * Returns the number of runs currently occupying a concurrency slot.
     * Derived from the permit holders rather than the semaphore's free count so
     * it reflects live runs exactly — the liveness probe surfaces this, and it
     * used to read ~0 under real load because permits were released as soon as
     * submission finished.
     */
    public int activeTestCount() {
        return permitHolders.size();
    }

    /**
     * Returns a run's concurrency permit exactly once. Safe to call from any
     * terminal path (reconciler, reaper, submission failure) and safe to call
     * repeatedly — only the holder that wins the {@code remove} releases.
     */
    private void releasePermit(String runId) {
        // Every terminal path funnels through here, so this is the one place
        // that reliably runs when a run ends. The duration metric normally
        // consumes the entry first; the reaper and the submission-failure path
        // did not, so their runs leaked one map entry each, forever.
        runStartNanos.remove(runId);
        if (permitHolders.remove(runId)) {
            concurrencyGuard.release();
        }
    }

    /**
     * Returns the configured maximum concurrent test limit.
     */
    public int maxConcurrentTests() {
        return maxConcurrentTests;
    }

    /**
     * Drives active runs to their terminal state without waiting for a client
     * to poll. Backend workers finish on their own schedule; nothing else calls
     * {@link #refreshStatus} unless a client GETs the run, so without this a
     * completed run stays RUNNING in the DB until the timeout reaper wrongly
     * marks it FAILED. Runs off the scheduler thread; refreshStatus is
     * idempotent for terminal runs (see the priorStatus guard).
     */
    @Scheduled(every = "{kates.engine.reconcile-interval:5s}", identity = "test-status-reconciler")
    void reconcileActiveRuns() {
        for (String runId : activeHandles.keySet()) {
            try {
                refreshStatus(runId);
            } catch (Exception e) {
                LOG.debugf("Status reconcile failed for run %s: %s", runId, e.getMessage());
            }
        }
    }

    /**
     * Stops any live backend workers for a run and drops its handles WITHOUT
     * changing the persisted status. Used by the timeout reaper, which owns the
     * FAILED transition — the previous reaper updated the DB row but left the
     * producer/consumer virtual threads running, so a "failed" run kept
     * hammering Kafka and skewing concurrent runs.
     */
    public void abortWorkers(TestRun run) {
        List<BenchmarkHandle> handles = activeHandles.remove(run.getId());
        // The run is ending either way, so its meters and concurrency slot must
        // be reclaimed even when there is nothing left to stop.
        benchmarkMetrics.endRun(run.getId());
        releasePermit(run.getId());
        if (handles == null || handles.isEmpty()) {
            return;
        }
        String backendName = run.getBackend() != null ? run.getBackend() : defaultBackend;
        com.bmscomp.kates.util.Result<BenchmarkBackend, Exception> backendResult = resolveBackend(backendName);
        if (backendResult.isFailure()) {
            return;
        }
        BenchmarkBackend backend = backendResult.asSuccess().orElseThrow();
        for (BenchmarkHandle handle : handles) {
            try {
                backend.stop(handle);
            } catch (Exception e) {
                LOG.warnf("Reaper: failed to stop task %s: %s", handle.taskId(), e.getMessage());
            }
        }
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
            runStartNanos.remove(runId);
        }
        activeHandles.clear();
        heatmapRows.clear();
        heatmapOrder.clear();
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
        List<LatencyHeatmapData.HeatmapRow> rows = heatmapRows.get(runId);
        if (rows == null) {
            return List.of();
        }
        synchronized (rows) {
            return List.copyOf(rows);
        }
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
                .orElseGet(() -> com.bmscomp.kates.util.Result.failure(new BenchmarkException(
                        "Backend not found: '" + name + "'. Available: " + availableBackends())));
    }

    @io.opentelemetry.instrumentation.annotations.WithSpan("TestOrchestrator.buildTasks")
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
                        produceTask(runId + "-produce-0", runId, topic, spec, producerConfig),
                        consumeTask(runId + "-consume-0", runId, topic, spec));
            case STRESS -> {
                var tasks = new java.util.ArrayList<BenchmarkTask>();
                for (int i = 0; i < spec.getNumProducers(); i++) {
                    tasks.add(produceTask(runId + "-stress-" + i, runId, topic, spec, producerConfig));
                }
                yield tasks;
            }
            case SPIKE ->
                List.of(BenchmarkTask.builder(runId + "-spike-burst", BenchmarkTask.WorkloadType.PRODUCE)
                        .runId(runId)
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
                        produceTask(runId + "-endurance-produce", runId, topic, spec, producerConfig),
                        consumeTask(runId + "-endurance-consume", runId, topic, spec));
            case VOLUME -> List.of(produceTask(runId + "-volume-0", runId, topic, spec, producerConfig));
            case CAPACITY -> {
                var tasks = new java.util.ArrayList<BenchmarkTask>();
                for (int i = 0; i < spec.getNumProducers(); i++) {
                    tasks.add(BenchmarkTask.builder(runId + "-cap-" + i, BenchmarkTask.WorkloadType.PRODUCE)
                            .runId(runId)
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
                        .runId(runId)
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
                        .runId(runId)
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
                List.of(produceTask(runId + "-tune-0", runId, topic, spec, producerConfig));
            case INTEGRATION_CDC ->
                List.of(BenchmarkTask.builder(runId + "-integration-cdc", BenchmarkTask.WorkloadType.INTEGRITY_CDC)
                        .runId(runId)
                        .topic(topic)
                        .producerConfig(producerConfig)
                        .build());
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
            case WARMUP, STEADY, COOLDOWN ->
                List.of(produceTask(taskId + "-produce", runId, topic, spec, producerConfig));
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
                    tasks.add(produceTask(taskId + "-ramp-" + s, runId, topic, stepSpec, producerConfig));
                }
                yield tasks;
            }
            case SPIKE ->
                List.of(BenchmarkTask.builder(taskId + "-spike", BenchmarkTask.WorkloadType.PRODUCE)
                        .runId(runId)
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

    /**
     * Ends consumers that are waiting for records nobody will ever send.
     *
     * <p>The producer and the consumer of a run share a run id and nothing else:
     * they are submitted together and then run independently. So when the
     * producer died on its first record, the consumer went on polling an empty
     * topic for the run's full duration — ten minutes by default — before
     * reporting anything at all. The whole run sat at RUNNING with 0 records
     * while the reason for it had already been decided in the first second.
     *
     * <p>The condition is deliberately narrow: every producer in the run has
     * finished, at least one failed, and between them they sent nothing. Only
     * then is an empty topic a certainty rather than a slow start.
     */
    private List<TestResult> abortStrandedConsumers(
            List<TestResult> results, BenchmarkBackend backend, Map<String, BenchmarkHandle> handleMap) {

        boolean anyProducer = false;
        boolean allProducersFinished = true;
        boolean anyProducerFailed = false;
        double producedRecords = 0;

        for (TestResult r : results) {
            if (!isProducer(r)) {
                continue;
            }
            anyProducer = true;
            producedRecords += r.getRecordsSent();
            if (r.getStatus() == TestResult.TaskStatus.FAILED) {
                anyProducerFailed = true;
            } else if (r.getStatus() != TestResult.TaskStatus.DONE) {
                allProducersFinished = false;
            }
        }

        if (!anyProducer || !allProducersFinished || !anyProducerFailed || producedRecords > 0) {
            return results;
        }

        List<TestResult> updated = new java.util.ArrayList<>(results.size());
        for (TestResult r : results) {
            boolean stranded = "consume".equals(r.getPhaseName())
                    && r.getStatus() != TestResult.TaskStatus.DONE
                    && r.getStatus() != TestResult.TaskStatus.FAILED;
            if (!stranded) {
                updated.add(r);
                continue;
            }

            BenchmarkHandle handle = handleMap.get(r.getTaskId());
            if (handle != null) {
                try {
                    backend.stop(handle);
                } catch (Exception e) {
                    LOG.warn("Failed to stop stranded consumer: " + r.getTaskId(), e);
                }
            }
            LOG.warnf("Aborting consumer %s: every producer in this run failed without sending", r.getTaskId());
            updated.add(r.withStatus(TestResult.TaskStatus.FAILED)
                    .withError("Aborted: every producer in this run failed without sending a record,"
                            + " so nothing would ever arrive on the topic.")
                    .withEndTime(Instant.now().toString()));
        }
        return updated;
    }

    private static boolean isProducer(TestResult result) {
        String phase = result.getPhaseName();
        return "produce".equals(phase) || "round-trip".equals(phase) || "integrity".equals(phase);
    }

    /**
     * A name for the row this task will occupy in a result table.
     *
     * <p>Only scenario phases used to set one, so every task in an ordinary run
     * fell back to the CLI's placeholder. A LOAD run therefore printed two rows
     * both labelled "main" — one FAILED and one RUNNING, with no way to tell
     * which was the producer.
     */
    private static String phaseNameFor(BenchmarkTask task) {
        return switch (task.getWorkloadType()) {
            case PRODUCE -> "produce";
            case CONSUME -> "consume";
            case ROUND_TRIP -> "round-trip";
            case INTEGRITY -> "integrity";
            case INTEGRITY_CDC -> "integrity-cdc";
        };
    }

    private BenchmarkTask produceTask(
            String taskId, String runId, String topic, TestSpec spec, Map<String, String> producerConfig) {
        return BenchmarkTask.builder(taskId, BenchmarkTask.WorkloadType.PRODUCE)
                .runId(runId)
                .topic(topic)
                .partitions(spec.getPartitions())
                .targetMessagesPerSec(spec.getThroughput())
                .maxMessages(spec.getNumRecords())
                .durationMs(spec.getDurationMs())
                .recordSize(spec.getRecordSize())
                .producerConfig(producerConfig)
                .build();
    }

    private BenchmarkTask consumeTask(String taskId, String runId, String topic, TestSpec spec) {
        return BenchmarkTask.builder(taskId, BenchmarkTask.WorkloadType.CONSUME)
                .runId(runId)
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
