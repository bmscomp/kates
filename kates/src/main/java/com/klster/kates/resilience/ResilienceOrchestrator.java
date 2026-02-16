package com.klster.kates.resilience;

import com.klster.kates.chaos.ChaosCoordinator;
import com.klster.kates.chaos.ChaosOutcome;
import com.klster.kates.domain.TestResult;
import com.klster.kates.domain.TestRun;
import com.klster.kates.engine.TestOrchestrator;
import com.klster.kates.report.ReportGenerator;
import com.klster.kates.report.ReportSummary;
import com.klster.kates.report.TestReport;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;
import java.util.logging.Level;
import java.util.logging.Logger;

/**
 * Orchestrates a combined performance + chaos resilience test.
 *
 * Flow:
 * 1. Start benchmark
 * 2. Wait for steady-state period
 * 3. Inject fault via ChaosCoordinator
 * 4. Wait for fault to complete
 * 5. Collect results and compute pre/post impact analysis
 */
@ApplicationScoped
public class ResilienceOrchestrator {

    private static final Logger LOG = Logger.getLogger(ResilienceOrchestrator.class.getName());

    @Inject
    TestOrchestrator testOrchestrator;

    @Inject
    ChaosCoordinator chaosCoordinator;

    @Inject
    ReportGenerator reportGenerator;

    public ResilienceReport execute(ResilienceTestRequest request) {
        ResilienceReport report = new ResilienceReport();

        try {
            // 1. Start the benchmark test
            LOG.info("Resilience test: starting benchmark");
            TestRun run = testOrchestrator.executeTest(request.getTestRequest());

            // 2. Wait for steady state (non-blocking delay for virtual threads)
            LOG.info("Resilience test: waiting " + request.getSteadyStateSec() + "s for steady state");
            CompletableFuture.runAsync(() -> {}, CompletableFuture.delayedExecutor(
                    request.getSteadyStateSec(), TimeUnit.SECONDS)).join();

            // 3. Snapshot pre-chaos results
            testOrchestrator.refreshStatus(run.getId());
            ReportSummary preChaos = computePartialSummary(run);
            report.setPreChaosSummary(preChaos);

            // 4. Inject fault
            LOG.info("Resilience test: injecting fault '" + request.getChaosSpec().experimentName() + "'");
            CompletableFuture<ChaosOutcome> chaosFuture = chaosCoordinator.triggerFault(request.getChaosSpec());

            // 5. Wait for chaos to complete (with timeout)
            ChaosOutcome outcome = chaosFuture.get(
                    request.getChaosSpec().chaosDurationSec() + 60, TimeUnit.SECONDS);
            report.setChaosOutcome(outcome);

            // 6. Let the system recover briefly (non-blocking delay)
            CompletableFuture.runAsync(() -> {}, CompletableFuture.delayedExecutor(
                    10, TimeUnit.SECONDS)).join();
            testOrchestrator.refreshStatus(run.getId());

            // 7. Generate final report
            TestReport perfReport = reportGenerator.generate(run);
            report.setPerformanceReport(perfReport);

            ReportSummary postChaos = perfReport.getSummary();
            report.setPostChaosSummary(postChaos);

            // 8. Compute impact deltas
            if (preChaos != null && postChaos != null) {
                report.setImpactDeltas(computeImpactDeltas(preChaos, postChaos));
            }

            // 9. Extract integrity result if present (INTEGRITY test type)
            run.getResults().stream()
                    .filter(r -> r.getIntegrity() != null)
                    .findFirst()
                    .ifPresent(r -> report.setIntegrityResult(r.getIntegrity()));

            report.setStatus(outcome.isPass() ? "COMPLETED" : "CHAOS_FAILED");

        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            report.setStatus("INTERRUPTED");
            LOG.log(Level.WARNING, "Resilience test interrupted", e);
        } catch (Exception e) {
            report.setStatus("ERROR");
            LOG.log(Level.SEVERE, "Resilience test failed", e);
        }

        return report;
    }

    private ReportSummary computePartialSummary(TestRun run) {
        List<TestResult> results = run.getResults();
        if (results == null || results.isEmpty()) {
            return new ReportSummary(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0);
        }

        long totalRecords = results.stream().mapToLong(TestResult::getRecordsSent).sum();
        double avgThroughput = results.stream().mapToDouble(TestResult::getThroughputRecordsPerSec).average().orElse(0);
        double peakThroughput = results.stream().mapToDouble(TestResult::getThroughputRecordsPerSec).max().orElse(0);
        double avgThroughputMB = results.stream().mapToDouble(TestResult::getThroughputMBPerSec).average().orElse(0);
        double avgLatency = results.stream().mapToDouble(TestResult::getAvgLatencyMs).average().orElse(0);
        double p50 = results.stream().mapToDouble(TestResult::getP50LatencyMs).average().orElse(0);
        double p95 = results.stream().mapToDouble(TestResult::getP95LatencyMs).average().orElse(0);
        double p99 = results.stream().mapToDouble(TestResult::getP99LatencyMs).average().orElse(0);
        double max = results.stream().mapToDouble(TestResult::getMaxLatencyMs).max().orElse(0);
        long errors = results.stream().filter(r -> r.getError() != null).count();
        double errorRate = totalRecords > 0 ? (double) errors / totalRecords : 0;

        return new ReportSummary(totalRecords, avgThroughput, peakThroughput, avgThroughputMB,
                avgLatency, p50, p95, p99, 0, max, errors, errorRate, 0);
    }

    private Map<String, Double> computeImpactDeltas(ReportSummary pre, ReportSummary post) {
        Map<String, Double> deltas = new LinkedHashMap<>();
        deltas.put("throughputRecPerSec", pctChange(pre.avgThroughputRecPerSec(), post.avgThroughputRecPerSec()));
        deltas.put("avgLatencyMs", pctChange(pre.avgLatencyMs(), post.avgLatencyMs()));
        deltas.put("p99LatencyMs", pctChange(pre.p99LatencyMs(), post.p99LatencyMs()));
        deltas.put("maxLatencyMs", pctChange(pre.maxLatencyMs(), post.maxLatencyMs()));
        deltas.put("errorRate", pctChange(pre.errorRate(), post.errorRate()));
        return deltas;
    }

    private double pctChange(double base, double current) {
        if (base == 0) return current == 0 ? 0 : 100.0;
        return ((current - base) / base) * 100.0;
    }
}
