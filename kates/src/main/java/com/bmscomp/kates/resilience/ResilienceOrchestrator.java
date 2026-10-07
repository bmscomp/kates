package com.bmscomp.kates.resilience;

import java.time.Duration;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.stream.Collectors;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.context.control.ActivateRequestContext;
import jakarta.inject.Inject;

import org.jboss.logging.Logger;

import com.bmscomp.kates.chaos.*;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.report.ReportGenerator;
import com.bmscomp.kates.report.ReportSummary;
import com.bmscomp.kates.report.TestReport;
import com.bmscomp.kates.util.MetricUtils;

/**
 * Orchestrates a combined performance + chaos resilience test with probe evaluation.
 *
 * Flow:
 * 1. Start benchmark, and wait for its tasks to be submitted
 * 2. Wait for steady-state period
 * 3. Evaluate baseline probes
 * 4. Inject fault via ChaosCoordinator
 * 5. Run continuous probes during chaos
 * 6. Wait for fault to complete
 * 7. Measure recovery time (RTO) via probe polling
 * 8. Collect results and compute pre/post impact analysis
 *
 * <p>The fault goes in only while the benchmark is RUNNING. One that ended
 * before it, or never started, ends the test ERROR after step 1 or 2: the
 * fault would hit a cluster with no load on it, and nothing after it would
 * measure anything.
 */
@ApplicationScoped
public class ResilienceOrchestrator {

    private static final Logger LOG = Logger.getLogger(ResilienceOrchestrator.class);

    @Inject
    TestOrchestrator testOrchestrator;

    @Inject
    ChaosCoordinator chaosCoordinator;

    @Inject
    ReportGenerator reportGenerator;

    @Inject
    ProbeExecutor probeExecutor;

    /**
     * How long to wait for a run's tasks to be submitted. Creating its topic
     * can take about two minutes to fail (four attempts of up to 30 s), and a
     * run still PENDING after this ends the test ERROR.
     */
    long submissionWaitMs = 300_000;

    /** How often a run whose tasks are being submitted is read again. */
    long submissionPollIntervalMs = 1_000;

    @ActivateRequestContext
    public ResilienceReport execute(ResilienceTestRequest request) {
        ResilienceReport report = new ResilienceReport();

        try {
            List<ProbeSpec> probes = resolveProbes(request);

            // 1. Start the benchmark test
            LOG.info("Resilience test: starting benchmark");
            var result = testOrchestrator.executeTest(request.getTestRequest());
            if (result.isFailure()) {
                String why = result.asFailure().orElseThrow().getMessage();
                report.setStatus("ERROR");
                report.setError("The benchmark did not start: " + why);
                LOG.error("Failed to start resilience benchmark: " + why);
                return report;
            }
            TestRun run = result.asSuccess().orElseThrow();

            // A scenario's tasks are submitted before executeTest answers, a
            // plain test's on a thread of their own: its run reads PENDING
            // until they are, and FAILED if submitting them failed.
            run = awaitSubmission(run);
            Optional<String> notRunning = whyNotRunning(run);
            if (notRunning.isPresent()) {
                return refuse(report, notRunning.get());
            }

            // 2. Wait for steady state
            LOG.info("Resilience test: waiting " + request.getSteadyStateSec() + "s for steady state");
            CompletableFuture.runAsync(
                            () -> {}, CompletableFuture.delayedExecutor(request.getSteadyStateSec(), TimeUnit.SECONDS))
                    .join();

            // 3. Snapshot pre-chaos results + evaluate baseline probes, unless
            // the run failed or finished during the wait.
            run = testOrchestrator.refreshStatus(run.getId());
            notRunning = whyNotRunning(run);
            if (notRunning.isPresent()) {
                return refuse(report, notRunning.get());
            }
            ReportSummary preChaos = MetricUtils.computeSummary(run.getResults());
            report.setPreChaosSummary(preChaos);

            if (!probes.isEmpty()) {
                String namespace = request.getChaosSpec().targetNamespace();
                LOG.info("Resilience test: evaluating " + probes.size() + " baseline probes");
                List<ProbeResult> baseline = probeExecutor.evaluateAll(probes, namespace);
                report.setBaselineProbes(baseline);
                long passCount = baseline.stream().filter(ProbeResult::passed).count();
                LOG.infof("Baseline probes: %d/%d passed", passCount, baseline.size());
            }

            // 4. Inject fault + start continuous probes
            LOG.info("Resilience test: injecting fault '"
                    + request.getChaosSpec().experimentName() + "'");

            List<ProbeResult> duringChaosResults = new CopyOnWriteArrayList<>();
            AtomicBoolean chaosActive = new AtomicBoolean(true);

            if (!probes.isEmpty()) {
                startContinuousProbes(
                        probes, request.getChaosSpec().targetNamespace(), chaosActive, duringChaosResults);
            }

            // An integrity task measures RPO back from the moment the fault
            // goes in, which the provider reports once the fault's delay is
            // over. Marked then, not when the fault's future completes: the
            // task may finish verifying first. Taken before the trigger, the
            // mark came up to delayBeforeSec early. Noop injects nothing and
            // reports nothing, so RPO is reported as not measured, not zero.
            String runId = run.getId();
            CompletableFuture<ChaosOutcome> chaosFuture = chaosCoordinator.triggerFault(
                    request.getChaosSpec(), injectedAt -> testOrchestrator.markChaosStart(runId, injectedAt));

            // 5. Wait for chaos to complete, its delay included: both chaos
            // providers wait that out before they inject.
            FaultSpec chaosSpec = request.getChaosSpec();
            ChaosOutcome outcome =
                    chaosFuture.get(chaosSpec.delayBeforeSec() + chaosSpec.chaosDurationSec() + 120, TimeUnit.SECONDS);
            chaosActive.set(false);
            report.setChaosOutcome(outcome);
            report.setDuringChaosProbes(List.copyOf(duringChaosResults));

            long duringPass =
                    duringChaosResults.stream().filter(ProbeResult::passed).count();
            LOG.infof("During-chaos probes: %d/%d passed", duringPass, duringChaosResults.size());

            // 6. Measure recovery time (RTO), when a poll finds every probe
            // passing before the wait is over
            if (!probes.isEmpty()) {
                String namespace = request.getChaosSpec().targetNamespace();
                Recovery recovery = awaitRecovery(probes, namespace, request.getMaxRecoveryWaitSec());
                if (recovery.recovered()) {
                    report.setRecoveryTime(recovery.after());
                    LOG.infof("Recovery time (RTO): %dms", recovery.after().toMillis());
                } else {
                    report.setUnrecoveredAfter(recovery.after());
                    LOG.warnf(
                            "No recovery: a probe still failed %dms after the fault",
                            recovery.after().toMillis());
                }

                List<ProbeResult> postRecovery = recovery.lastPoll();
                report.setPostRecoveryProbes(postRecovery);
                long postPass =
                        postRecovery.stream().filter(ProbeResult::passed).count();
                LOG.infof("Post-recovery probes: %d/%d passed", postPass, postRecovery.size());
            } else {
                CompletableFuture.runAsync(() -> {}, CompletableFuture.delayedExecutor(10, TimeUnit.SECONDS))
                        .join();
            }

            run = testOrchestrator.refreshStatus(run.getId());

            // 7. Generate final report
            TestReport perfReport = reportGenerator.generate(run);
            report.setPerformanceReport(perfReport);

            ReportSummary postChaos = perfReport.getSummary();
            report.setPostChaosSummary(postChaos);

            // 8. Compute impact deltas
            if (preChaos != null && postChaos != null) {
                Map<String, Double> deltas = new LinkedHashMap<>();
                deltas.put(
                        "throughputRecPerSec",
                        MetricUtils.pctChange(preChaos.avgThroughputRecPerSec(), postChaos.avgThroughputRecPerSec()));
                deltas.put("avgLatencyMs", MetricUtils.pctChange(preChaos.avgLatencyMs(), postChaos.avgLatencyMs()));
                deltas.put("p99LatencyMs", MetricUtils.pctChange(preChaos.p99LatencyMs(), postChaos.p99LatencyMs()));
                deltas.put("maxLatencyMs", MetricUtils.pctChange(preChaos.maxLatencyMs(), postChaos.maxLatencyMs()));
                deltas.put("errorRate", MetricUtils.pctChange(preChaos.errorRate(), postChaos.errorRate()));
                report.setImpactDeltas(deltas);
            }

            // 9. Extract integrity result if present
            run.getResults().stream()
                    .filter(r -> r.getIntegrity() != null)
                    .findFirst()
                    .ifPresent(r -> report.setIntegrityResult(r.getIntegrity()));

            report.setStatus(outcome.isPass() ? "COMPLETED" : "CHAOS_FAILED");

        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            report.setStatus("INTERRUPTED");
            LOG.warn("Resilience test interrupted", e);
        } catch (Exception e) {
            report.setStatus("ERROR");
            report.setError(
                    e.getMessage() != null ? e.getMessage() : e.getClass().getName());
            LOG.error("Resilience test failed", e);
        }

        return report;
    }

    private List<ProbeSpec> resolveProbes(ResilienceTestRequest request) {
        if (request.getProbes() != null && !request.getProbes().isEmpty()) {
            return request.getProbes();
        }
        if (request.getChaosSpec() != null) {
            return ProbeRegistry.resolve(request.getChaosSpec());
        }
        return List.of();
    }

    /** The run once its tasks are submitted, or once {@link #submissionWaitMs} has passed. */
    private TestRun awaitSubmission(TestRun run) throws InterruptedException {
        long deadline = System.nanoTime() + submissionWaitMs * 1_000_000L;
        while (run.getStatus() == TestResult.TaskStatus.PENDING && System.nanoTime() < deadline) {
            Thread.sleep(submissionPollIntervalMs);
            run = testOrchestrator.refreshStatus(run.getId());
        }
        return run;
    }

    /** Why the fault must not go in, or empty while the benchmark runs. */
    private Optional<String> whyNotRunning(TestRun run) {
        return switch (run.getStatus()) {
            case RUNNING -> Optional.empty();
            case PENDING ->
                Optional.of("The benchmark had not started " + submissionWaitMs / 1000 + " s after it was created: run "
                        + run.getId() + " is still PENDING, so no fault was injected");
            default ->
                Optional.of("The benchmark ended before the fault: run " + run.getId() + " is " + run.getStatus()
                        + ", so no fault was injected" + taskErrors(run));
        };
    }

    /** The run's task errors as a sentence to append, or "" when its tasks report none. */
    private static String taskErrors(TestRun run) {
        String errors = run.getResults().stream()
                .filter(r -> r.getError() != null && !r.getError().isBlank())
                .map(r -> r.getTaskId() + ": " + r.getError())
                .collect(Collectors.joining("; "));
        return errors.isEmpty() ? "" : ". Task errors: " + errors;
    }

    /**
     * The report, ERROR with {@code why}, for a benchmark the fault must not go
     * into. The baseline probes are not run either: they are what the probes
     * during and after the fault are compared with, and there will be none.
     * Run one after another, a command probe waiting up to its timeoutSec
     * (30 s by default) in a broker pod, they would only hold up the answer;
     * the task errors in {@code why} say why the benchmark ended.
     */
    private static ResilienceReport refuse(ResilienceReport report, String why) {
        report.setStatus("ERROR");
        report.setError(why);
        LOG.error("Resilience test: " + why);
        return report;
    }

    private void startContinuousProbes(
            List<ProbeSpec> probes, String namespace, AtomicBoolean active, List<ProbeResult> results) {

        List<ProbeSpec> continuousProbes =
                probes.stream().filter(p -> "Continuous".equals(p.mode())).toList();

        if (continuousProbes.isEmpty()) return;

        Thread.ofVirtual().name("probe-monitor").start(() -> {
            while (active.get()) {
                try {
                    List<ProbeResult> batch = probeExecutor.evaluateAll(continuousProbes, namespace);
                    results.addAll(batch);
                    int intervalSec = continuousProbes.getFirst().intervalSec();
                    Thread.sleep(intervalSec * 1000L);
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                    break;
                }
            }
        });
    }

    /** How long the recovery wait sleeps between two polls of the probes. */
    long recoveryPollIntervalMs = 5_000;

    /**
     * How the recovery wait ended: whether its last poll found every probe
     * passing, that poll's results, and how long after the end of the fault
     * the poll ended.
     */
    private record Recovery(boolean recovered, List<ProbeResult> lastPoll, Duration after) {}

    /**
     * Polls every probe until all of them pass in one poll, {@code maxWaitSec}
     * / 5 times at most and once at least, {@link #recoveryPollIntervalMs}
     * apart. The wait used to answer the time it had run for whether a poll
     * passed or not, so a run that never recovered reported the whole wait as
     * its recovery time, and a wait under 5 s made no poll and reported about
     * 0. An interrupt ends the wait with no answer: the run is INTERRUPTED.
     */
    private Recovery awaitRecovery(List<ProbeSpec> probes, String namespace, int maxWaitSec)
            throws InterruptedException {
        Instant chaosEnd = Instant.now();
        int polls = Math.max(1, maxWaitSec / 5);
        for (int poll = 1; ; poll++) {
            List<ProbeResult> results = probeExecutor.evaluateAll(probes, namespace);
            boolean recovered = results.stream().allMatch(ProbeResult::passed);
            if (recovered || poll >= polls) {
                return new Recovery(recovered, results, Duration.between(chaosEnd, Instant.now()));
            }
            Thread.sleep(recoveryPollIntervalMs);
        }
    }
}
