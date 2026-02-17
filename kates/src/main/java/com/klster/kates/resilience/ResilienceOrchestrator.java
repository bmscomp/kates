package com.klster.kates.resilience;

import com.klster.kates.chaos.ChaosCoordinator;
import com.klster.kates.chaos.ChaosOutcome;
import com.klster.kates.domain.TestRun;
import com.klster.kates.engine.TestOrchestrator;
import com.klster.kates.report.ReportGenerator;
import com.klster.kates.report.ReportSummary;
import com.klster.kates.report.TestReport;
import com.klster.kates.util.MetricUtils;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import java.util.LinkedHashMap;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;
import org.jboss.logging.Logger;

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

    private static final Logger LOG = Logger.getLogger(ResilienceOrchestrator.class);

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
            ReportSummary preChaos = MetricUtils.computeSummary(run.getResults());
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
                Map<String, Double> deltas = new LinkedHashMap<>();
                deltas.put("throughputRecPerSec", MetricUtils.pctChange(preChaos.avgThroughputRecPerSec(), postChaos.avgThroughputRecPerSec()));
                deltas.put("avgLatencyMs", MetricUtils.pctChange(preChaos.avgLatencyMs(), postChaos.avgLatencyMs()));
                deltas.put("p99LatencyMs", MetricUtils.pctChange(preChaos.p99LatencyMs(), postChaos.p99LatencyMs()));
                deltas.put("maxLatencyMs", MetricUtils.pctChange(preChaos.maxLatencyMs(), postChaos.maxLatencyMs()));
                deltas.put("errorRate", MetricUtils.pctChange(preChaos.errorRate(), postChaos.errorRate()));
                report.setImpactDeltas(deltas);
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
            LOG.warn("Resilience test interrupted", e);
        } catch (Exception e) {
            report.setStatus("ERROR");
            LOG.error("Resilience test failed", e);
        }

        return report;
    }

}
