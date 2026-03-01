package com.bmscomp.kates.resilience;

import java.time.Duration;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import org.jboss.logging.Logger;

import com.bmscomp.kates.chaos.*;
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
 * 1. Start benchmark
 * 2. Wait for steady-state period
 * 3. Evaluate baseline probes
 * 4. Inject fault via ChaosCoordinator
 * 5. Run continuous probes during chaos
 * 6. Wait for fault to complete
 * 7. Measure recovery time (RTO) via probe polling
 * 8. Collect results and compute pre/post impact analysis
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

    public ResilienceReport execute(ResilienceTestRequest request) {
        ResilienceReport report = new ResilienceReport();

        try {
            List<ProbeSpec> probes = resolveProbes(request);

            // 1. Start the benchmark test
            LOG.info("Resilience test: starting benchmark");
            var result = testOrchestrator.executeTest(request.getTestRequest());
            if (result.isFailure()) {
                report.setStatus("ERROR");
                LOG.error("Failed to start resilience benchmark: " + result.asFailure().orElseThrow().getMessage());
                return report;
            }
            TestRun run = result.asSuccess().orElseThrow();

            // 2. Wait for steady state
            LOG.info("Resilience test: waiting " + request.getSteadyStateSec() + "s for steady state");
            CompletableFuture.runAsync(
                            () -> {}, CompletableFuture.delayedExecutor(request.getSteadyStateSec(), TimeUnit.SECONDS))
                    .join();

            // 3. Snapshot pre-chaos results + evaluate baseline probes
            testOrchestrator.refreshStatus(run.getId());
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
                startContinuousProbes(probes, request.getChaosSpec().targetNamespace(),
                        chaosActive, duringChaosResults);
            }

            CompletableFuture<ChaosOutcome> chaosFuture = chaosCoordinator.triggerFault(request.getChaosSpec());

            // 5. Wait for chaos to complete
            ChaosOutcome outcome = chaosFuture.get(request.getChaosSpec().chaosDurationSec() + 60, TimeUnit.SECONDS);
            chaosActive.set(false);
            report.setChaosOutcome(outcome);
            report.setDuringChaosProbes(List.copyOf(duringChaosResults));

            long duringPass = duringChaosResults.stream().filter(ProbeResult::passed).count();
            LOG.infof("During-chaos probes: %d/%d passed", duringPass, duringChaosResults.size());

            // 6. Measure recovery time (RTO)
            if (!probes.isEmpty()) {
                String namespace = request.getChaosSpec().targetNamespace();
                Duration rto = measureRecoveryTime(probes, namespace, request.getMaxRecoveryWaitSec());
                report.setRecoveryTime(rto);
                LOG.infof("Recovery time (RTO): %dms", rto.toMillis());

                List<ProbeResult> postRecovery = probeExecutor.evaluateAll(probes, namespace);
                report.setPostRecoveryProbes(postRecovery);
                long postPass = postRecovery.stream().filter(ProbeResult::passed).count();
                LOG.infof("Post-recovery probes: %d/%d passed", postPass, postRecovery.size());
            } else {
                CompletableFuture.runAsync(() -> {}, CompletableFuture.delayedExecutor(10, TimeUnit.SECONDS))
                        .join();
            }

            testOrchestrator.refreshStatus(run.getId());

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

    private void startContinuousProbes(
            List<ProbeSpec> probes,
            String namespace,
            AtomicBoolean active,
            List<ProbeResult> results) {

        List<ProbeSpec> continuousProbes = probes.stream()
                .filter(p -> "Continuous".equals(p.mode()))
                .toList();

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

    private Duration measureRecoveryTime(List<ProbeSpec> probes, String namespace, int maxWaitSec) {
        Instant chaosEnd = Instant.now();
        int attempts = 0;
        int maxAttempts = maxWaitSec / 5;

        while (attempts < maxAttempts) {
            List<ProbeResult> results = probeExecutor.evaluateAll(probes, namespace);
            boolean allPassing = results.stream().allMatch(ProbeResult::passed);

            if (allPassing) {
                return Duration.between(chaosEnd, Instant.now());
            }

            attempts++;
            try {
                Thread.sleep(5000);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                break;
            }
        }

        return Duration.between(chaosEnd, Instant.now());
    }
}
