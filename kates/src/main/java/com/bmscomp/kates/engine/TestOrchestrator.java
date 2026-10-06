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
    /**
     * Evaluates the run's SLA against each poll so the violations reach
     * Prometheus while the run is still going. The report path evaluates the
     * same definition through the same class once the run is over, which is
     * the only reason both can be trusted to agree.
     */
    private final SlaEvaluator slaEvaluator;

    private final Event<TestLifecycleEvent> lifecycleEvents;
    private final String defaultBackend;
    private final String bootstrapServers;
    private final int maxConcurrentTests;
    /**
     * The longest a run may be set to last (see {@link #plannedDurationMs}),
     * and the most the timeout reaper allows any run before its grace. A
     * request planned for longer is refused rather than started and cut short.
     */
    private final long maxDurationMs;

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
            SlaEvaluator slaEvaluator,
            Event<TestLifecycleEvent> lifecycleEvents,
            @ConfigProperty(name = "kates.engine.default-backend", defaultValue = "native") String defaultBackend,
            @ConfigProperty(name = "kates.kafka.bootstrap-servers") String bootstrapServers,
            @ConfigProperty(name = "kates.engine.max-concurrent-tests", defaultValue = "3") int maxConcurrentTests,
            @ConfigProperty(name = "kates.engine.max-duration-ms", defaultValue = "7200000") long maxDurationMs) {
        this.topicService = topicService;
        this.repository = repository;
        this.backends = backends;
        this.typeDefaults = typeDefaults;
        this.benchmarkMetrics = benchmarkMetrics;
        this.katesMetrics = katesMetrics;
        this.slaEvaluator = slaEvaluator;
        this.lifecycleEvents = lifecycleEvents;
        this.defaultBackend = defaultBackend;
        this.bootstrapServers = bootstrapServers;
        this.maxConcurrentTests = maxConcurrentTests;
        this.maxDurationMs = maxDurationMs;
        this.concurrencyGuard = new Semaphore(maxConcurrentTests);
    }

    @PostConstruct
    void recoverOrphans() {
        List<TestRun> orphans = repository.findByStatus(TestResult.TaskStatus.RUNNING);
        if (orphans.isEmpty()) {
            return;
        }
        LOG.infof("Recovering %d orphaned RUNNING tests from previous lifecycle", orphans.size());
        for (TestRun orphan : orphans) {
            // findByStatus reads runs without their task results. Read the run
            // whole, so that each task that had not finished is failed with the
            // reason and every task is written back with what it measured.
            TestRun run = repository.findById(orphan.getId()).orElse(null);
            if (run == null) {
                continue;
            }
            run = run.withStatus(TestResult.TaskStatus.FAILED);
            List<TestResult> newResults = new java.util.ArrayList<>();
            for (TestResult result : run.getResults()) {
                if (result.getStatus() == TestResult.TaskStatus.RUNNING
                        || result.getStatus() == TestResult.TaskStatus.PENDING) {
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

        // Before the permit: a request that cannot run as written is the
        // caller's to fix, and must not cost a slot another run could use.
        // Before applyTypeDefaults too, which throws on a request with no type.
        java.util.Optional<InvalidTestSpecException> refused = refusal(request);
        if (refused.isPresent()) {
            return com.bmscomp.kates.util.Result.failure(refused.get());
        }

        TestType type = request.getType();
        TestSpec spec = applyTypeDefaults(type, request.getSpec());
        String backendName = request.getBackend() != null ? request.getBackend() : defaultBackend;

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

        TestRun run = new TestRun(type, spec)
                .withBackend(backendName)
                .withRequestedSpec(explicitFieldsOf(request.getSpec()))
                .withPlannedDurationMs(plannedDurationMs(type, spec));
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
        var handles = new java.util.ArrayList<BenchmarkHandle>();
        try {
            createTestTopic(spec, type);
            List<BenchmarkTask> tasks = buildTasks(type, spec, run.getId());
            run = run.withStatus(TestResult.TaskStatus.RUNNING);
            fireEvent(run, TestLifecycleEvent.EventKind.RUNNING);
            benchmarkMetrics.startRun(run.getId(), type.name(), backendName);

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
                    // Never polled, so this is its only chance to be counted
                    // while the run is live.
                    benchmarkMetrics.recordTaskStatus(
                            run.getId(), task.getTaskId(), phaseNameFor(task), TestResult.TaskStatus.FAILED);
                }
            }

            submitted = handles;

            boolean allFailed = run.getResults().stream().allMatch(r -> r.getStatus() == TestResult.TaskStatus.FAILED);
            if (allFailed) {
                run = run.withStatus(TestResult.TaskStatus.FAILED);
            }

        } catch (Exception e) {
            LOG.error("Test execution failed for run: " + run.getId(), e);
            run = failSubmission(run, handles, e);
        }

        // Registered only AFTER the row carrying these tasks is persisted.
        // Publishing the handles first let the 5s reconciler read the run and
        // write its own version of the row while this method was still building
        // it — a lost update, or an optimistic-lock failure thrown into the
        // virtual thread. The finally matters: workers are already running by
        // now, so a failed save must not leave them with no handle to stop them.
        //
        // An update only: the client has the run's id from executeTest's
        // answer, so a delete can land while the tasks are being submitted, and
        // a plain save would insert the deleted run again, RUNNING.
        boolean stored = false;
        try {
            stored = repository.saveIfPresent(run);
        } finally {
            registerHandles(run, submitted);
        }
        if (!stored) {
            // Deleted while its tasks were being submitted. The delete settled
            // the run before these workers had handles to stop, so stop them now.
            LOG.infof("Run %s was deleted while its tasks were being submitted; stopping them", run.getId());
            settle(run.getId());
        } else if (run.getStatus() == TestResult.TaskStatus.FAILED) {
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
        String backendName = scenarioBackend(request);

        // Refused before the permit, as in executeTest.
        java.util.Optional<InvalidTestSpecException> refused = refusal(request);
        if (refused.isPresent()) {
            return com.bmscomp.kates.util.Result.failure(refused.get());
        }

        // Built before the permit too: a throw between taking the permit and
        // recording the run as its holder stranded the permit until a restart,
        // as a scenario with no type did here, in applyTypeDefaults.
        TestSpec baseSpec = applyTypeDefaults(type, scenario.getBaseSpec());
        TestRun run = new TestRun(type, baseSpec)
                .withRequestedSpec(explicitFieldsOf(scenario.getBaseSpec()))
                .withBackend(backendName)
                .withScenarioName(scenario.getName())
                .withLabels(scenario.getLabels())
                .withSla(scenario.getSla())
                .withPlannedDurationMs(plannedDurationMs(scenario))
                .withStatus(TestResult.TaskStatus.RUNNING);

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
        var allHandles = new java.util.ArrayList<BenchmarkHandle>();

        try {
            createTestTopic(baseSpec, type);
            benchmarkMetrics.startRun(run.getId(), type.name(), backendName);

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
                        benchmarkMetrics.recordTaskStatus(
                                run.getId(), task.getTaskId(), phaseName, TestResult.TaskStatus.FAILED);
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
            run = failSubmission(run, allHandles, e);
        }

        // Same ordering rule as executeAsync: publish handles only once the row
        // they belong to is persisted, so the reconciler cannot race this write
        // — but publish them even if that write fails, so running workers stay
        // stoppable. An update only, as there: the row is listed from the
        // first save, so a delete can come before this one.
        boolean stored = false;
        try {
            stored = repository.saveIfPresent(run);
        } finally {
            registerHandles(run, submitted);
        }
        if (!stored) {
            LOG.infof("Run %s was deleted while its phases were being submitted; stopping them", run.getId());
            settle(run.getId());
        } else if (run.getStatus() == TestResult.TaskStatus.FAILED) {
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
        java.util.Optional<TestRun> found = repository.findById(runId);
        if (found.isEmpty()) {
            // Gone from the store while this process may still be running it:
            // its submission registered workers after a delete had settled it,
            // or its row was deleted outside the API. The reconciler used to
            // meet it here every tick and leave its workers running and its
            // permit taken until a restart, and nothing else would settle it.
            settle(runId);
            throw new IllegalArgumentException("Test run not found: " + runId);
        }
        TestRun run = found.get();

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
        // Every violation this poll found, across every task, merged before it
        // is published: recording per task would have the last task polled
        // clear the violations of the ones before it.
        List<com.bmscomp.kates.domain.SlaViolation> slaViolations = new java.util.ArrayList<>();
        boolean polledAnything = false;
        for (TestResult result : run.getResults()) {
            if (result.getStatus() == TestResult.TaskStatus.RUNNING
                    || result.getStatus() == TestResult.TaskStatus.PENDING) {
                BenchmarkHandle handle = handleMap.get(result.getTaskId());
                if (handle != null) {
                    try {
                        BenchmarkStatus status = backend.poll(handle);
                        result = applyStatus(result, status);
                        polledAnything = true;
                        publishLiveMetrics(runId, result, status);
                        slaViolations.addAll(
                                slaEvaluator.evaluate(run.getSla(), status).violations());

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

        if (polledAnything) {
            benchmarkMetrics.recordSlaViolations(runId, slaViolations);
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
                    // The per-run counter takes the task's final cumulative
                    // total. Safe to repeat what the last poll already
                    // published: the phase counter clamps each task's total
                    // upward, so this can only close a gap, never double-count.
                    benchmarkMetrics.recordRecords(runId, r.getTaskId(), r.getPhaseName(), r.getRecordsSent());
                }
                // Counted once per task, so a failure the live poll already
                // published is not counted twice here; this pass exists for
                // the tasks no poll ever reported FAILED, such as a consumer
                // aborted by abortStrandedConsumers.
                benchmarkMetrics.recordTaskStatus(runId, r.getTaskId(), r.getPhaseName(), r.getStatus());
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
                // An update only. A delete that lands while this poll is under
                // way must stay a delete: a plain save inserts the run again.
                if (!repository.saveIfPresent(run)) {
                    settle(runId);
                }
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
     * The run, FAILED, for a submission that threw part-way: each task it had
     * already started is stopped, and failed with the reason.
     *
     * <p>Their handles are registered only once every task is submitted, so
     * such a submission left them none: a delete found nothing to stop, and a
     * FAILED run cannot be cancelled. Registering them would not have done it
     * either, since the reconciler drops a FAILED run's handles without
     * stopping them. A scenario whose later phase failed to build used to leave
     * the phases before it producing for their whole duration, 600 s by
     * default, with their tasks RUNNING in the FAILED run.
     */
    private TestRun failSubmission(TestRun run, List<BenchmarkHandle> started, Exception cause) {
        stopTasks(run.getId(), started);
        return withUnfinishedTasksFailed(
                run.withStatus(TestResult.TaskStatus.FAILED),
                "Stopped: the run failed while its tasks were being submitted: " + cause);
    }

    /**
     * Publishes a run's backend handles so the reconciler, the reaper and
     * shutdown can poll and stop its workers.
     *
     * <p>Registered even when the run already looks terminal, though that keeps
     * its workers stoppable only until the reconciler's next tick: the terminal
     * path in {@link #refreshStatus(String)} drops the entry without stopping
     * them. So a submission that throws part-way stops the tasks it started
     * itself ({@link #failSubmission}).
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
     * Ends what a run holds in this process: stops its backend workers, ends
     * its per-run meters, drops its handles and gives back its concurrency
     * permit, WITHOUT touching the stored row. Whoever settles the run owns
     * what happens to the row: cancel and the timeout reaper store it FAILED,
     * a delete removes it, and the reconciler settles a run whose row is gone.
     *
     * <p>Idempotent, so every path can call it, for a run already settled or
     * one this process never tracked. The previous reaper updated the row but
     * left the producer/consumer virtual threads running, so a "failed" run
     * kept hammering Kafka and skewing concurrent runs.
     *
     * <p>Each handle is stopped through the backend that issued it, which is
     * the run's own: the row that names the run's backend may be gone already.
     */
    public void settle(String runId) {
        List<BenchmarkHandle> handles = activeHandles.remove(runId);
        // The run is ending either way, so its meters and concurrency slot must
        // be reclaimed even when there is nothing left to stop, and before a
        // backend that fails to stop can get in the way.
        benchmarkMetrics.endRun(runId);
        releasePermit(runId);
        if (handles == null) {
            return;
        }
        stopTasks(runId, handles);
    }

    /**
     * Stops each task through the backend that issued it. A task that cannot
     * be stopped is logged, and the others are still stopped.
     */
    private void stopTasks(String runId, List<BenchmarkHandle> handles) {
        for (BenchmarkHandle handle : handles) {
            java.util.Optional<BenchmarkBackend> backend =
                    resolveBackend(handle.backendName()).asSuccess();
            if (backend.isEmpty()) {
                LOG.warnf(
                        "Cannot stop task %s of ended run %s: no backend %s",
                        handle.taskId(), runId, handle.backendName());
                continue;
            }
            try {
                backend.get().stop(handle);
            } catch (Exception e) {
                LOG.warnf("Failed to stop task %s of ended run: %s", handle.taskId(), e.getMessage());
            }
        }
    }

    /**
     * Deletes a run and its results, first settling it if it is still going:
     * its tasks stop and its concurrency permit comes back. Every delete path
     * used to remove the row without settling the run, and the reconciler,
     * which then found the run gone every tick, never settled it either. Its
     * workers could keep producing, and enough such deletes made every new run
     * answer 429 until a restart.
     *
     * <p>Settled first, so the reconciler stops tracking the run before its
     * row goes. A write already under way cannot bring the row back, because
     * the run's own writes only update a row (see
     * {@link TestRunRepository#saveIfPresent}). Workers a submission still
     * under way registers after the settle are stopped by whichever finds the
     * row gone first: that submission's write, or the reconciler.
     *
     * <p>A run deleted before it ended is announced as ending FAILED, as a
     * cancelled run is, with the detail "deleted": the delete queues the
     * webhooks' event with the row's removal, and the event stream and the
     * completion metric hear of it here, once the row is gone. Whether the run
     * had ended is what the delete read under the row's lock, not a status
     * read before it, so a run that ended on its own in between is not
     * announced twice. An ended run's delete announces nothing.
     *
     * @return false when no run has that id
     */
    public boolean deleteTest(String runId) {
        settle(runId);
        java.util.Optional<TestRun> deleted = repository.delete(runId);
        if (deleted.isEmpty()) {
            return false;
        }
        TestResult.TaskStatus status = deleted.get().getStatus();
        if (status != TestResult.TaskStatus.DONE && status != TestResult.TaskStatus.FAILED) {
            String typeName = deleted.get().getTestType() != null
                    ? deleted.get().getTestType().name()
                    : "UNKNOWN";
            lifecycleEvents.fireAsync(
                    new TestLifecycleEvent(runId, typeName, TestLifecycleEvent.EventKind.FAILED, "deleted"));
            katesMetrics.recordTestCompleted(typeName, "failed");
        }
        return true;
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

    /**
     * Tells a run's live tasks that a fault was injected at
     * {@code chaosStartNanos} ({@link System#nanoTime()} in this JVM), so an
     * integrity task can measure RPO from it. Tasks that have already finished
     * are past the point where it could change their result.
     */
    public void markChaosStart(String runId, long chaosStartNanos) {
        for (BenchmarkHandle handle : activeHandles.getOrDefault(runId, List.of())) {
            resolveBackend(handle.backendName())
                    .asSuccess()
                    .ifPresent(backend -> backend.markChaosStart(handle, chaosStartNanos));
        }
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
     *
     * <p>Every field the request can set is carried. The merge used to copy
     * fourteen and drop the other seven (targetThroughput, consumerGroup, the
     * two fetch settings and the three integrity options), so they were
     * accepted, echoed at their defaults and never used.
     *
     * <p>{@code throughput} is the rate the producers honour, and
     * {@code targetThroughput} is another name for it: the name the CLI and
     * scenario files send. When both are set, {@code throughput} wins; when only
     * {@code targetThroughput} is, it sets the rate, in place of the type's
     * default. So the merged {@code throughput} is always the rate the run
     * used, and {@code targetThroughput} stays as requested: absent, like the
     * other fields a type has no default for, when the request left it out.
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
            if (userSpec.hasTargetThroughput()) {
                merged.setTargetThroughput(userSpec.getTargetThroughput());
                merged.setThroughput(userSpec.getTargetThroughput());
            }
            if (userSpec.hasThroughput()) merged.setThroughput(userSpec.getThroughput());
            if (userSpec.hasDurationMs()) merged.setDurationMs(userSpec.getDurationMs());
            if (userSpec.hasNumProducers()) merged.setNumProducers(userSpec.getNumProducers());
            if (userSpec.hasNumConsumers()) merged.setNumConsumers(userSpec.getNumConsumers());
            if (userSpec.hasConsumerGroup()) merged.setConsumerGroup(userSpec.getConsumerGroup());
            if (userSpec.hasFetchMinBytes()) merged.setFetchMinBytes(userSpec.getFetchMinBytes());
            if (userSpec.hasFetchMaxWaitMs()) merged.setFetchMaxWaitMs(userSpec.getFetchMaxWaitMs());
            if (userSpec.hasEnableIdempotence()) merged.setEnableIdempotence(userSpec.isEnableIdempotence());
            if (userSpec.hasEnableTransactions()) merged.setEnableTransactions(userSpec.isEnableTransactions());
            if (userSpec.hasEnableCrc()) merged.setEnableCrc(userSpec.isEnableCrc());
        }

        return merged;
    }

    /** The request's own fields, or none when it sent no spec. */
    private static Map<String, Object> explicitFieldsOf(TestSpec requested) {
        return requested != null ? requested.explicitFields() : Map.of();
    }

    /** The backend a scenario runs on: the scenario's, the request's, or the default. */
    private String scenarioBackend(CreateTestRequest request) {
        TestScenario scenario = request.getScenario();
        if (scenario.getBackend() != null) {
            return scenario.getBackend();
        }
        return request.getBackend() != null ? request.getBackend() : defaultBackend;
    }

    /**
     * Why a run could not honour this request as written, or empty when it
     * could, for a plain request or a scenario: no type to run as, a null
     * where a scenario phase should be, the spec fields its type and backend
     * cannot apply, and a length past {@code kates.engine.max-duration-ms},
     * the most the timeout reaper allows a run. executeTest fails with this
     * exception before it takes a concurrency permit. A caller that starts the
     * run later, as a resilience run does after it has begun streaming its
     * answer, asks first so that it can still answer the client with a 400.
     *
     * <p>POST /api/tests requires a type through bean validation, but POST
     * /api/resilience runs none, and neither does a schedule as it fires, so
     * the type is checked here too. It is keyed {@code type}, with no prefix:
     * the request's own field, which a scenario's type overrides.
     *
     * <p>A scenario value that the run would store and the Kates API's
     * database cannot hold is refused too: see {@link #unstorable}.
     */
    public java.util.Optional<InvalidTestSpecException> refusal(CreateTestRequest request) {
        // The type the run would have: the scenario's, or else the request's.
        TestType type = request.isScenario() && request.getScenario().getType() != null
                ? request.getScenario().getType()
                : request.getType();
        if (type == null) {
            String why = request.isScenario()
                    ? "neither the request nor its scenario has a type; set the request's type, or the scenario's,"
                    : "the request has no type; set it";
            return java.util.Optional.of(
                    new InvalidTestSpecException("", Map.of("type", why + " to one of " + typeNames())));
        }
        if (request.isScenario()) {
            TestScenario scenario = request.getScenario();
            // The checks below read every phase, so a null one threw there.
            Map<String, String> nulls = new java.util.LinkedHashMap<>();
            for (int i = 0; i < scenario.getPhases().size(); i++) {
                if (scenario.getPhases().get(i) == null) {
                    nulls.put(
                            "phases[" + i + "]",
                            "null is not a phase; remove it, or put a phase with a phaseType in its place");
                }
            }
            if (!nulls.isEmpty()) {
                return java.util.Optional.of(new InvalidTestSpecException("scenario.", nulls));
            }
            Map<String, String> errors = unstorable(scenario);
            errors.putAll(scenarioInapplicableFields(scenario, scenarioBackend(request)));
            long planned = plannedDurationMs(scenario);
            if (planned > maxDurationMs) {
                errors.put("phases", "the phases are set to last " + planned + " ms in all" + longerThanAllowed());
            }
            return errors.isEmpty()
                    ? java.util.Optional.empty()
                    : java.util.Optional.of(new InvalidTestSpecException("scenario.", errors));
        }
        String backendName = request.getBackend() != null ? request.getBackend() : defaultBackend;
        TestSpec merged = applyTypeDefaults(type, request.getSpec());
        Map<String, String> errors = inapplicableFields(type, backendName, request.getSpec(), merged);
        Long planned = plannedDurationMs(type, merged);
        if (planned != null && planned > maxDurationMs) {
            // The duration may be the type's default rather than the request's,
            // so say which; an INTEGRITY run's length is twice it.
            String source =
                    request.getSpec() != null && request.getSpec().hasDurationMs() ? "" : " (the type's default)";
            String length = type == TestType.INTEGRITY
                    ? "an INTEGRITY run produces for durationMs, " + merged.getDurationMs() + " ms" + source
                            + ", then reads its records back for up to as long again, " + planned + " ms in all"
                    : "the run is set to last " + planned + " ms" + source;
            errors.put("durationMs", length + longerThanAllowed());
        }
        return errors.isEmpty()
                ? java.util.Optional.empty()
                : java.util.Optional.of(new InvalidTestSpecException(errors));
    }

    /** The most characters of a scenario's name the run stores (test_runs.scenario_name). */
    private static final int MAX_SCENARIO_NAME = 128;

    /**
     * The most characters of a phase's name. Each id of the phase's tasks
     * holds it, after the run's id and before a suffix such as -produce or
     * -ramp-99, and the run stores an id in 128 characters
     * (test_results.task_id).
     */
    private static final int MAX_PHASE_NAME = 100;

    private static final String NUL_REASON =
            " holds a NUL character, which the Kates API's database cannot store; remove it";

    /**
     * The values of a scenario that its run would store and the Kates API's
     * database cannot hold, each keyed by its path in the scenario: a name
     * longer than the database keeps, and a NUL character in a name or a
     * label, which PostgreSQL stores in neither text nor jsonb. Each failed
     * the save that stores it. The scenario's name and labels are saved as
     * the run is registered, and the answer named none of it. A phase's name
     * is saved only once the phase's tasks have started, so the client got a
     * 500 while the tasks ran their course with nothing recorded, and the run
     * kept its slot until the reaper failed it.
     */
    private static Map<String, String> unstorable(TestScenario scenario) {
        Map<String, String> errors = new java.util.LinkedHashMap<>();
        String name = scenario.getName();
        if (name != null && characters(name) > MAX_SCENARIO_NAME) {
            errors.put(
                    "name",
                    "a scenario's name is stored in " + MAX_SCENARIO_NAME + " characters at most, and this one has "
                            + characters(name) + "; shorten it");
        } else if (holdsNul(name)) {
            errors.put("name", "a scenario's name" + NUL_REASON);
        }
        if (scenario.getLabels() != null
                && scenario.getLabels().entrySet().stream()
                        .anyMatch(label -> holdsNul(label.getKey()) || holdsNul(label.getValue()))) {
            errors.put("labels", "a label's key or value" + NUL_REASON);
        }
        for (int i = 0; i < scenario.getPhases().size(); i++) {
            String phaseName = scenario.getPhases().get(i).getName();
            if (phaseName != null && characters(phaseName) > MAX_PHASE_NAME) {
                errors.put(
                        "phases[" + i + "].name",
                        "a phase's name is " + MAX_PHASE_NAME + " characters at most, since the ids of its tasks hold"
                                + " it, and this one has " + characters(phaseName) + "; shorten it");
            } else if (holdsNul(phaseName)) {
                errors.put("phases[" + i + "].name", "a phase's name" + NUL_REASON);
            }
        }
        return errors;
    }

    /** Its length as PostgreSQL counts it, one for each code point. */
    private static int characters(String value) {
        return value.codePointCount(0, value.length());
    }

    private static boolean holdsNul(String value) {
        return value != null && value.indexOf('\0') >= 0;
    }

    private String longerThanAllowed() {
        return "; the Kates API allows a run at most " + maxDurationMs + " ms (kates.engine.max-duration-ms)";
    }

    /** Every test type a request can name: "LOAD, STRESS, ... or INTEGRATION_CDC". */
    private static String typeNames() {
        List<String> names =
                java.util.Arrays.stream(TestType.values()).map(TestType::name).toList();
        return String.join(", ", names.subList(0, names.size() - 1)) + " or " + names.getLast();
    }

    /**
     * How long a run of this type is set to last with this merged spec, or
     * null when no duration bounds it. Stored with the run: the timeout reaper
     * fails the run once this and a grace have passed since its creation.
     *
     * <p>Every task stops at its spec's durationMs, if its record count has not
     * stopped it first. An INTEGRITY task then reads its records back, for up
     * to durationMs again (NativeKafkaBackend.runIntegrity), so it can last
     * twice as long. An INTEGRATION_CDC task takes no duration: the CDC service
     * ends it on timeouts of its own.
     */
    static Long plannedDurationMs(TestType type, TestSpec spec) {
        if (type == null || spec == null || type == TestType.INTEGRATION_CDC) {
            return null;
        }
        long duration = Math.max(0, spec.getDurationMs());
        return type == TestType.INTEGRITY ? saturatedSum(duration, duration) : duration;
    }

    /**
     * How long a scenario is set to last: its phases' durations added up.
     * executeScenario submits the phases together, so they overlap and this is
     * an upper bound; it stays the right one if they come to run in sequence.
     */
    static long plannedDurationMs(TestScenario scenario) {
        long total = 0;
        for (ScenarioPhase phase : scenario.getPhases()) {
            total = saturatedSum(
                    total, Math.max(0, scenario.resolveSpecForPhase(phase).getDurationMs()));
        }
        return total;
    }

    /**
     * The sum of two longs that are not negative, held at Long.MAX_VALUE: a
     * scenario's spec is not validated, so its durations can be anything, and
     * a sum that wrapped negative would pass for a short run.
     */
    private static long saturatedSum(long a, long b) {
        long sum = a + b;
        return sum < 0 ? Long.MAX_VALUE : sum;
    }

    /**
     * The requested fields a run of this type on this backend could not
     * honour, each with the reason, keyed by field name; empty when there are
     * none.
     *
     * <p>Only what the request set is checked, against the merged spec the run
     * would use. A value that asks for nothing passes, because the run honours
     * it trivially: an unlimited rate (-1) for a type that always runs
     * unthrottled or runs no producer, {@code enableCrc: false} for a type that
     * checks no CRC, {@code false} for a producer option where there is no
     * producer. A value the run would contradict fails, and the request with
     * it, rather than the run going ahead on other terms.
     */
    Map<String, String> inapplicableFields(TestType type, String backendName, TestSpec requested, TestSpec merged) {
        Map<String, String> errors = new java.util.LinkedHashMap<>();
        if (requested == null || type == null) {
            return errors;
        }

        boolean producer = type != TestType.INTEGRATION_CDC;
        boolean consumerSettings = type == TestType.LOAD || type == TestType.ENDURANCE || type == TestType.INTEGRITY;
        boolean unthrottled = type == TestType.SPIKE || type == TestType.CAPACITY;

        if (!producer || unthrottled) {
            String why = producer
                    ? type + " runs its producers unthrottled whatever the rate says; only -1 (unlimited) applies"
                    : "INTEGRATION_CDC runs no Kates producer, so no rate applies; only -1 (unlimited) does";
            if (requested.hasThroughput() && requested.getThroughput() != -1) {
                errors.put("throughput", why);
            }
            if (requested.hasTargetThroughput() && requested.getTargetThroughput() != -1) {
                errors.put("targetThroughput", why);
            }
        }

        if (!consumerSettings) {
            String why = type == TestType.ROUND_TRIP
                    ? "ROUND_TRIP's consumer reads every partition without a group, with the client's fetch"
                            + " defaults; only LOAD, ENDURANCE and INTEGRITY take consumer settings"
                    : type + " starts no consumer; only LOAD, ENDURANCE and INTEGRITY do";
            if (requested.hasConsumerGroup()) errors.put("consumerGroup", why);
            if (requested.hasFetchMinBytes()) errors.put("fetchMinBytes", why);
            if (requested.hasFetchMaxWaitMs()) errors.put("fetchMaxWaitMs", why);
        }

        if (requested.hasEnableCrc() && requested.isEnableCrc() && type != TestType.INTEGRITY) {
            errors.put("enableCrc", "only an INTEGRITY run checks record CRCs; a " + type + " run checks none");
        }

        if (!producer) {
            String why = "INTEGRATION_CDC runs no Kates producer to configure";
            if (requested.isEnableIdempotence()) errors.put("enableIdempotence", why);
            if (requested.isEnableTransactions()) errors.put("enableTransactions", why);
            return errors;
        }

        // The Kafka client refuses both options with any acks but all, so the
        // run would fail as its producer starts. The acks may be the type's
        // default rather than the request's (SPIKE's is 1), so name it.
        String acks = merged.getAcks();
        boolean acksAll = "all".equals(acks) || "-1".equals(acks);
        String acksWhy = " needs acks=all, and this " + type + " run's acks is " + acks
                + (requested.hasAcks() ? "" : " (the type's default); set acks to all");
        if (requested.isEnableIdempotence() && !acksAll) {
            errors.put("enableIdempotence", "an idempotent producer" + acksWhy);
        }
        if (requested.isEnableTransactions()) {
            if (!acksAll) {
                errors.put("enableTransactions", "a transactional producer" + acksWhy);
            } else if (requested.hasEnableIdempotence() && !requested.isEnableIdempotence()) {
                errors.put(
                        "enableTransactions",
                        "a transactional producer is always idempotent, and the request sets enableIdempotence to false");
            } else if ("trogdor".equals(backendName)) {
                errors.put("enableTransactions", "the trogdor backend cannot run a transactional producer");
            }
        }
        return errors;
    }

    /**
     * The most steps a RAMP phase may have. buildPhaseTask starts a producer
     * for each step, all at once, so this bounds the producers of one phase as
     * {@code numProducers} (at most 100) bounds those of a plain request.
     */
    static final int MAX_RAMP_STEPS = 100;

    /**
     * The scenario's fields its phases could not honour, keyed by their path
     * in the scenario ({@code baseSpec.x}, {@code phases[i].spec.x},
     * {@code phases[i].x}); empty when there are none.
     *
     * <p>Every phase is a set of producers (buildPhaseTask), whatever the
     * scenario's type, so the consumer settings and CRC checks have nothing to
     * apply to; they were stored as the run's spec and never used. The two
     * producer options reach every phase, and are checked against the acks
     * each phase resolves, since the Kafka client refuses them with any other.
     * The rate is checked against each phase's type (refuseInapplicableRate).
     *
     * <p>A phase's type decides the producers it starts, and nothing else
     * checks that it has one: buildPhaseTask threw on a phase without one
     * after the run was stored, so the run ended FAILED, and the phases
     * submitted before it ran on with no handle to stop them. Only a RAMP
     * phase reads rampSteps; any other starts one producer, so there a
     * rampSteps other than its default of 1 is refused rather than ignored.
     */
    Map<String, String> scenarioInapplicableFields(TestScenario scenario, String backendName) {
        Map<String, String> errors = new java.util.LinkedHashMap<>();
        List<ScenarioPhase> phases = scenario.getPhases();
        refuseConsumerAndCrc("baseSpec.", scenario.getBaseSpec(), errors);
        for (int i = 0; i < phases.size(); i++) {
            refuseConsumerAndCrc("phases[" + i + "].spec.", phases.get(i).getSpec(), errors);
        }

        for (int i = 0; i < phases.size(); i++) {
            ScenarioPhase phase = phases.get(i);
            TestSpec own = phase.getSpec();
            TestSpec resolved = scenario.resolveSpecForPhase(phase);
            String name = phase.getName() != null ? phase.getName() : "phase-" + i;
            ScenarioPhase.PhaseType type = phase.getPhaseType();
            if (type == null) {
                errors.put(
                        "phases[" + i + "].phaseType",
                        "phase " + name + " has no phaseType, which decides the producers it starts; set WARMUP,"
                                + " RAMP, STEADY, SPIKE or COOLDOWN");
            } else if (type != ScenarioPhase.PhaseType.RAMP && phase.getRampSteps() != 1) {
                errors.put(
                        "phases[" + i + "].rampSteps",
                        "phase " + name + " is a " + type + " phase, which starts one producer whatever rampSteps"
                                + " says; only a RAMP phase has steps, so only 1 applies");
            }
            // Named where it was set: the phase's own spec, or the base one.
            String idempotence = own != null && own.hasEnableIdempotence()
                    ? "phases[" + i + "].spec.enableIdempotence"
                    : "baseSpec.enableIdempotence";
            String transactions = own != null && own.hasEnableTransactions()
                    ? "phases[" + i + "].spec.enableTransactions"
                    : "baseSpec.enableTransactions";
            String acks = resolved.getAcks();
            boolean acksAll = "all".equals(acks) || "-1".equals(acks);
            String acksWhy = " needs acks=all, and phase " + name + " runs with acks " + acks;
            if (resolved.isEnableIdempotence() && !acksAll) {
                errors.putIfAbsent(idempotence, "an idempotent producer" + acksWhy);
            }
            if (resolved.isEnableTransactions()) {
                if (!acksAll) {
                    errors.putIfAbsent(transactions, "a transactional producer" + acksWhy);
                } else if (resolved.hasEnableIdempotence() && !resolved.isEnableIdempotence()) {
                    errors.putIfAbsent(
                            transactions,
                            "a transactional producer is always idempotent, and phase " + name
                                    + " sets enableIdempotence to false");
                } else if ("trogdor".equals(backendName)) {
                    errors.putIfAbsent(transactions, "the trogdor backend cannot run a transactional producer");
                }
            }
            refuseInapplicableRate("phases[" + i + "].", name, phase, resolved, errors);
        }
        return errors;
    }

    /**
     * Refuses the rate settings a phase of its type could not honour.
     *
     * <p>A SPIKE phase runs its producer unthrottled whatever the rate says, so
     * a rate the phase sets itself is refused, as for a plain SPIKE request; a
     * rate it inherits from the base spec is there for the other phases.
     *
     * <p>A RAMP phase divides its rate into rampSteps steps: step k runs at k
     * times the rate divided by rampSteps, and at least k records a second. A
     * phase resolves its rate from the base spec as sent, without the type's
     * defaults, so one that no rate reached resolved -1, and its steps ran at
     * 1, 2, ... records a second, sending almost nothing while it looked like
     * a ramp; with fewer records a second than steps, the last steps ran past
     * the rate. So the rate has to come to at least one record a second per
     * step. The steps are producers started together, at most MAX_RAMP_STEPS.
     * A rampSteps below 1 ran as one step, the whole rate from the start, so
     * it is refused too.
     */
    private static void refuseInapplicableRate(
            String path, String name, ScenarioPhase phase, TestSpec resolved, Map<String, String> errors) {
        if (phase.getPhaseType() == ScenarioPhase.PhaseType.SPIKE) {
            String why = "phase " + name + " is a SPIKE phase, which runs its producer unthrottled whatever the"
                    + " rate says; only -1 (unlimited) applies";
            TestSpec own = phase.getSpec();
            if (phase.getTargetThroughput() != -1) {
                errors.put(path + "targetThroughput", why);
            }
            if (own != null && own.hasThroughput() && own.getThroughput() != -1) {
                errors.put(path + "spec.throughput", why);
            }
            if (own != null && own.hasTargetThroughput() && own.getTargetThroughput() != -1) {
                errors.put(path + "spec.targetThroughput", why);
            }
            return;
        }
        if (phase.getPhaseType() != ScenarioPhase.PhaseType.RAMP) {
            return;
        }
        int steps = Math.max(1, phase.getRampSteps());
        int rate = resolved.getThroughput();
        String perStep = "a RAMP phase needs a rate of at least one record a second per step, and phase " + name;
        if (phase.getRampSteps() < 1) {
            errors.put(
                    path + "rampSteps",
                    "a RAMP phase starts a producer for each step, and phase " + name + " has " + phase.getRampSteps()
                            + " steps; it needs at least 1");
        }
        if (steps > MAX_RAMP_STEPS) {
            errors.put(
                    path + "rampSteps",
                    "a RAMP phase starts a producer for each step, all at once, and phase " + name + " has " + steps
                            + " steps; it may have at most " + MAX_RAMP_STEPS);
        }
        if (rate < 1) {
            errors.put(
                    path + "targetThroughput",
                    perStep + " has none (" + rate + ", unlimited); set the phase's targetThroughput, or a"
                            + " throughput in its spec or the base spec");
        } else if (rate < steps) {
            errors.putIfAbsent(
                    path + "rampSteps",
                    perStep + " has " + steps + " steps for " + rate + " records a second; use at most " + rate
                            + " steps, or a higher rate");
        }
    }

    private static void refuseConsumerAndCrc(String path, TestSpec spec, Map<String, String> errors) {
        if (spec == null) {
            return;
        }
        String why = "a scenario's phases start no consumer; only a LOAD, ENDURANCE or INTEGRITY request"
                + " without phases does";
        if (spec.hasConsumerGroup()) errors.put(path + "consumerGroup", why);
        if (spec.hasFetchMinBytes()) errors.put(path + "fetchMinBytes", why);
        if (spec.hasFetchMaxWaitMs()) errors.put(path + "fetchMaxWaitMs", why);
        if (spec.hasEnableCrc() && spec.isEnableCrc()) {
            errors.put(
                    path + "enableCrc",
                    "a scenario's phases check no record CRCs; only an INTEGRITY request without phases does");
        }
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

        Map<String, String> producerConfig = new HashMap<>();
        producerConfig.put("bootstrap.servers", bootstrapServers);
        producerConfig.put("acks", spec.getAcks());
        producerConfig.put("batch.size", String.valueOf(spec.getBatchSize()));
        producerConfig.put("linger.ms", String.valueOf(spec.getLingerMs()));
        producerConfig.put("compression.type", spec.getCompressionType());
        // Only when asked. Left unset, the client decides, and with acks=all it
        // turns idempotence on by itself; an explicit false has to reach the
        // client to turn it off, which the task's flag alone cannot do.
        if (spec.hasEnableIdempotence()) {
            producerConfig.put("enable.idempotence", String.valueOf(spec.isEnableIdempotence()));
        }

        Map<String, String> consumerConfig = new HashMap<>();
        if (spec.hasFetchMinBytes()) {
            consumerConfig.put("fetch.min.bytes", String.valueOf(spec.getFetchMinBytes()));
        }
        if (spec.hasFetchMaxWaitMs()) {
            consumerConfig.put("fetch.max.wait.ms", String.valueOf(spec.getFetchMaxWaitMs()));
        }
        // A transactional producer's records are for read_committed readers;
        // the integrity consumer already reads that way, and so does a LOAD or
        // ENDURANCE consumer of a transactional run.
        if (spec.isEnableTransactions()) {
            consumerConfig.put("isolation.level", "read_committed");
        }

        return switch (type) {
            case LOAD ->
                List.of(
                        produceTask(runId + "-produce-0", runId, topic, spec, producerConfig),
                        consumeTask(runId + "-consume-0", runId, topic, spec, consumerConfig));
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
                        .enableIdempotence(spec.isEnableIdempotence())
                        .enableTransactions(spec.isEnableTransactions())
                        .build());
            case ENDURANCE ->
                List.of(
                        produceTask(runId + "-endurance-produce", runId, topic, spec, producerConfig),
                        consumeTask(runId + "-endurance-consume", runId, topic, spec, consumerConfig));
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
                            .enableIdempotence(spec.isEnableIdempotence())
                            .enableTransactions(spec.isEnableTransactions())
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
                        // Its consumer takes no fetch settings (inapplicableFields
                        // refuses them), but a transactional run's must read
                        // committed: an uncommitted record is not yet delivered.
                        .consumerConfig(consumerConfig)
                        .enableIdempotence(spec.isEnableIdempotence())
                        .enableTransactions(spec.isEnableTransactions())
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
                        // The backend's integrity consumer joins this name with
                        // "-integrity" appended (NativeKafkaBackend), so the
                        // default group is integrity-cg-integrity.
                        .consumerGroup(spec.getConsumerGroup() != null ? spec.getConsumerGroup() : "integrity-cg")
                        .producerConfig(producerConfig)
                        .consumerConfig(consumerConfig)
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
        // As in buildTasks: only when asked, so that an explicit false reaches
        // the client and an absent one leaves it to decide.
        if (spec.hasEnableIdempotence()) {
            producerConfig.put("enable.idempotence", String.valueOf(spec.isEnableIdempotence()));
        }

        String taskId = runId + "-" + phaseName;

        return switch (phase.getPhaseType()) {
            case WARMUP, STEADY, COOLDOWN ->
                List.of(produceTask(taskId + "-produce", runId, topic, spec, producerConfig));
            case RAMP -> {
                // refuseInapplicableRate has made sure of a rate of at least a
                // record a second per step, so no step runs past it.
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
                    stepSpec.setEnableIdempotence(spec.isEnableIdempotence());
                    stepSpec.setEnableTransactions(spec.isEnableTransactions());
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
                        .enableIdempotence(spec.isEnableIdempotence())
                        .enableTransactions(spec.isEnableTransactions())
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
                .enableIdempotence(spec.isEnableIdempotence())
                .enableTransactions(spec.isEnableTransactions())
                .build();
    }

    private BenchmarkTask consumeTask(
            String taskId, String runId, String topic, TestSpec spec, Map<String, String> consumerConfig) {
        return BenchmarkTask.builder(taskId, BenchmarkTask.WorkloadType.CONSUME)
                .runId(runId)
                .topic(topic)
                .partitions(spec.getPartitions())
                .maxMessages(spec.getNumRecords())
                .durationMs(spec.getDurationMs())
                // A named group that has committed offsets on the topic resumes
                // from them; the per-task default never has any.
                .consumerGroup(spec.getConsumerGroup() != null ? spec.getConsumerGroup() : taskId + "-group")
                .consumerConfig(consumerConfig)
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

    /**
     * Feeds one poll of one task into the run's Prometheus meters.
     *
     * <p>This is the live path, and until it existed the per-run meters only
     * ever held a value for the instant between the terminal transition writing
     * them and {@code endRun} unregistering them — so
     * {@code kates_benchmark_throughput_rec_sec} was, in practice, never
     * scraped with a value at all. Everything published here comes from the
     * poll the backend just answered; nothing is derived or estimated.
     *
     * <p>{@code getRecordsProcessed()} is the task's cumulative total, which is
     * what {@code recordRecords} wants — see its contract for why a delta would
     * be wrong.
     */
    private void publishLiveMetrics(String runId, TestResult result, BenchmarkStatus status) {
        // Registers the phase's error counter, so the error series exists from
        // the phase's first poll like the ones below, and counts a failure on
        // the poll that observes it — see recordTaskStatus for why counting
        // only at the terminal transition never reached a scrape.
        benchmarkMetrics.recordTaskStatus(runId, result.getTaskId(), result.getPhaseName(), status.getState());
        if (status.getThroughputRecordsPerSec() > 0 || status.getThroughputMBPerSec() > 0) {
            benchmarkMetrics.recordThroughput(
                    runId, result.getPhaseName(), status.getThroughputRecordsPerSec(), status.getThroughputMBPerSec());
        }
        if (status.getRecordsProcessed() > 0) {
            benchmarkMetrics.recordRecords(
                    runId, result.getTaskId(), result.getPhaseName(), status.getRecordsProcessed());
        }
        benchmarkMetrics.recordLatency(
                runId,
                result.getTaskId(),
                result.getPhaseName(),
                status.getP50LatencyMs(),
                status.getP95LatencyMs(),
                status.getP99LatencyMs(),
                status.getP999LatencyMs(),
                status.getMaxLatencyMs());
        // Only a chaos or resilience run carries one, and only from the poll
        // after the verifier has finished — which is why this is guarded rather
        // than published unconditionally. See recordIntegrity for why the
        // millisecond figures on IntegrityResult must not reach a _seconds
        // series unconverted.
        benchmarkMetrics.recordIntegrity(runId, status.getIntegrityResult());
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

    /** The error a cancel gives each task of the run that had not finished. */
    private static final String CANCELLED_TASK_ERROR = "Cancelled by user";

    /**
     * Cancels a PENDING or RUNNING run: stops its tasks, then stores the run
     * as FAILED, with each task that had not finished marked FAILED and given
     * {@link #CANCELLED_TASK_ERROR}. {@code TaskStatus} has no CANCELLED, so
     * FAILED is what a cancelled run reads back as; REST and gRPC both answer
     * with the run as stored here, so the answer and every later read agree.
     *
     * <p>It ends the run the way the timeout reaper does, because nothing
     * settles a FAILED run afterwards: {@link #refreshStatus} returns early for
     * it and the reaper only scans RUNNING. So {@link #settle} hands back
     * the concurrency slot and the per-run meters here, before FAILED is
     * written, since once the row reads FAILED the reconciler drops the run's
     * handles and nothing could stop its workers. The write is a compare-and-set
     * on the status read, so a run that ends on its own in between keeps its
     * ending; one that only moved from PENDING to RUNNING is cancelled on the
     * second pass.
     *
     * @return the run as stored, or empty when no run has that id
     * @throws RunNotCancellableException when the run is neither PENDING nor
     *     RUNNING, or ended while being cancelled
     */
    public java.util.Optional<TestRun> cancelTest(String runId) {
        TestResult.TaskStatus status = null;
        // Two passes cover the one move that keeps a run cancellable, PENDING
        // to RUNNING; a run that moved again has ended.
        for (int pass = 0; pass < 2; pass++) {
            java.util.Optional<TestRun> found = repository.findById(runId);
            if (found.isEmpty()) {
                return found;
            }
            TestRun run = found.get();
            status = run.getStatus();
            if (status != TestResult.TaskStatus.RUNNING && status != TestResult.TaskStatus.PENDING) {
                throw new RunNotCancellableException(status);
            }
            TestRun cancelled =
                    withUnfinishedTasksFailed(run.withStatus(TestResult.TaskStatus.FAILED), CANCELLED_TASK_ERROR);
            settle(runId);
            if (repository.saveIfStatus(cancelled, status)) {
                String typeName = cancelled.getTestType() != null
                        ? cancelled.getTestType().name()
                        : "UNKNOWN";
                // The terminal event the run would otherwise never get: an SSE
                // subscriber used to see STOPPING and then nothing.
                lifecycleEvents.fireAsync(
                        new TestLifecycleEvent(runId, typeName, TestLifecycleEvent.EventKind.FAILED, "cancelled"));
                katesMetrics.recordTestCompleted(typeName, "failed");
                return java.util.Optional.of(cancelled);
            }
        }
        TestResult.TaskStatus stored =
                repository.findById(runId).map(TestRun::getStatus).orElse(status);
        throw new RunNotCancellableException(stored);
    }

    /** The run with each task that had not finished marked FAILED with this error. */
    private static TestRun withUnfinishedTasksFailed(TestRun run, String error) {
        if (run.getResults() == null) {
            return run;
        }
        List<TestResult> updatedResults = new java.util.ArrayList<>();
        for (TestResult result : run.getResults()) {
            if (result.getStatus() == TestResult.TaskStatus.RUNNING
                    || result.getStatus() == TestResult.TaskStatus.PENDING) {
                result = result.withStatus(TestResult.TaskStatus.FAILED)
                        .withError(error)
                        .withEndTime(Instant.now().toString());
            }
            updatedResults.add(result);
        }
        return run.withResults(updatedResults);
    }
}
