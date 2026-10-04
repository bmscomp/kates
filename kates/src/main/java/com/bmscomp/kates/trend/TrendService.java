package com.bmscomp.kates.trend;

import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.*;
import java.util.function.Function;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.report.ReportGenerator;
import com.bmscomp.kates.report.ReportSummary;
import com.bmscomp.kates.report.TestReport;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.util.MetricUtils;

/**
 * Computes historical metric trends from completed test runs.
 * Supports rolling baselines, automatic regression detection,
 * and per-phase metric extraction for multi-phase test types.
 *
 * <p>A point is its run's report summary, or a phase's, computed from the
 * stored results as {@link ReportGenerator} computes it but without building
 * the report. A trend reads every run in its window, and a report not yet
 * cached describes the cluster for its broker figures, with 30-second
 * timeouts, and counts an SLA evaluation. Only the broker trend, which plots
 * those figures, builds reports.
 */
@ApplicationScoped
public class TrendService {

    @Inject
    TestRunRepository repository;

    @Inject
    ReportGenerator reportGenerator;

    /**
     * Compute trend for overall run metrics (backward-compatible).
     */
    public TrendResponse computeTrend(TestType type, String metric, int days, int baselineWindow) {
        return computeTrend(type, metric, days, baselineWindow, null);
    }

    /**
     * Compute trend for a specific metric, optionally scoped to a single phase.
     * When {@code phase} is non-null, the metric is extracted from the matching
     * phase's summary instead of the overall {@link ReportSummary}.
     * Runs that do not contain the requested phase are silently skipped.
     */
    public TrendResponse computeTrend(TestType type, String metric, int days, int baselineWindow, String phase) {
        List<TestRun> runs = findRuns(type, days);

        Function<ReportSummary, Double> extractor = metricExtractor(metric);
        if (extractor == null) {
            return phase != null
                    ? TrendResponse.empty(type.name(), metric, phase)
                    : TrendResponse.empty(type.name(), metric);
        }

        List<TrendResponse.DataPoint> dataPoints = new ArrayList<>();
        for (TestRun run : runs) {
            ReportSummary summary =
                    phase != null ? findPhaseSummary(run, phase) : MetricUtils.computeSummary(run.getResults());

            if (summary != null) {
                double value = extractor.apply(summary);
                dataPoints.add(new TrendResponse.DataPoint(run.getCreatedAt(), run.getId(), value));
            }
        }

        double baseline = computeBaseline(dataPoints, baselineWindow);
        List<TrendResponse.Regression> regressions = detectRegressions(dataPoints, baseline, metric);

        return new TrendResponse(type.name(), metric, phase, dataPoints, baseline, regressions);
    }

    /**
     * Compute trend breakdown for all phases in a single response.
     * Each distinct phase name found across matching runs gets its own
     * sparkline, baseline, and regression list.
     */
    public PhaseTrendResponse computeBreakdown(TestType type, String metric, int days, int baselineWindow) {
        List<TestRun> runs = findRuns(type, days);

        Function<ReportSummary, Double> extractor = metricExtractor(metric);
        if (extractor == null) {
            return new PhaseTrendResponse(type.name(), metric, List.of());
        }

        Map<String, List<TrendResponse.DataPoint>> byPhase = new LinkedHashMap<>();

        for (TestRun run : runs) {
            for (Map.Entry<String, ReportSummary> phase :
                    ReportGenerator.phaseSummaries(run.getResults()).entrySet()) {
                double value = extractor.apply(phase.getValue());
                byPhase.computeIfAbsent(phase.getKey(), k -> new ArrayList<>())
                        .add(new TrendResponse.DataPoint(run.getCreatedAt(), run.getId(), value));
            }
        }

        List<PhaseTrendResponse.PhaseTrend> phaseTrends = new ArrayList<>();
        for (Map.Entry<String, List<TrendResponse.DataPoint>> entry : byPhase.entrySet()) {
            double baseline = computeBaseline(entry.getValue(), baselineWindow);
            List<TrendResponse.Regression> regressions = detectRegressions(entry.getValue(), baseline, metric);
            phaseTrends.add(new PhaseTrendResponse.PhaseTrend(entry.getKey(), entry.getValue(), baseline, regressions));
        }

        return new PhaseTrendResponse(type.name(), metric, phaseTrends);
    }

    /**
     * Discover distinct phase names across completed runs of the given type.
     */
    public List<String> discoverPhases(TestType type, int days) {
        List<TestRun> runs = findRuns(type, days);
        Set<String> phases = new LinkedHashSet<>();

        for (TestRun run : runs) {
            phases.addAll(ReportGenerator.phaseSummaries(run.getResults()).keySet());
        }
        return new ArrayList<>(phases);
    }

    /**
     * Compute trend for a specific broker's projected metrics across test runs.
     * For each run, generates the report, finds the matching broker in the
     * broker metrics list, and extracts the requested metric value.
     */
    public BrokerTrendResponse computeBrokerTrend(
            TestType type, String metric, int brokerId, int days, int baselineWindow) {
        List<TestRun> runs = findRuns(type, days);

        Function<ReportSummary, Double> extractor = metricExtractor(metric);
        if (extractor == null) {
            BrokerTrendResponse resp = new BrokerTrendResponse();
            resp.setBrokerId(brokerId);
            resp.setTestType(type.name());
            resp.setMetric(metric);
            resp.setDataPoints(List.of());
            resp.setRegressions(List.of());
            return resp;
        }

        List<TrendResponse.DataPoint> dataPoints = new ArrayList<>();
        for (TestRun run : runs) {
            // Broker figures exist only in a report: they split the run's
            // summary by its topic's partition leaders, read when the report
            // is built.
            TestReport report = reportGenerator.generate(run);
            if (report.getBrokerMetrics() == null) continue;

            report.getBrokerMetrics().stream()
                    .filter(b -> b.brokerId() == brokerId)
                    .findFirst()
                    .ifPresent(bm -> {
                        double value = extractor.apply(bm.metrics());
                        dataPoints.add(new TrendResponse.DataPoint(run.getCreatedAt(), run.getId(), value));
                    });
        }

        double baseline = computeBaseline(dataPoints, baselineWindow);
        List<TrendResponse.Regression> regressions = detectRegressions(dataPoints, baseline, metric);

        BrokerTrendResponse resp = new BrokerTrendResponse();
        resp.setBrokerId(brokerId);
        resp.setTestType(type.name());
        resp.setMetric(metric);
        resp.setBaseline(baseline);
        resp.setDataPoints(dataPoints);
        resp.setRegressions(regressions);
        return resp;
    }

    /**
     * The runs a trend reads: the type's DONE runs created in the last
     * {@code days} days, oldest first, with their results.
     *
     * <p>DONE only. A FAILED run's numbers stop wherever it failed: beside
     * complete runs it reads as a regression, and in the baseline it can hide
     * a real one. A run still in flight has no final numbers yet.
     */
    private List<TestRun> findRuns(TestType type, int days) {
        Instant to = Instant.now();
        Instant from = to.minus(days, ChronoUnit.DAYS);
        return repository.findWithResults(type, TestResult.TaskStatus.DONE, from, to);
    }

    private ReportSummary findPhaseSummary(TestRun run, String phase) {
        return ReportGenerator.phaseSummaries(run.getResults()).entrySet().stream()
                .filter(e -> phase.equalsIgnoreCase(e.getKey()))
                .map(Map.Entry::getValue)
                .findFirst()
                .orElse(null);
    }

    private double computeBaseline(List<TrendResponse.DataPoint> points, int window) {
        if (points.isEmpty()) return 0;
        int start = Math.max(0, points.size() - window);
        return points.subList(start, points.size()).stream()
                .mapToDouble(TrendResponse.DataPoint::value)
                .average()
                .orElse(0);
    }

    private List<TrendResponse.Regression> detectRegressions(
            List<TrendResponse.DataPoint> points, double baseline, String metric) {
        if (baseline == 0) return List.of();

        boolean isLatencyMetric = metric.toLowerCase().contains("latency");
        double threshold = 0.20;

        List<TrendResponse.Regression> regressions = new ArrayList<>();
        for (TrendResponse.DataPoint point : points) {
            double deviation = (point.value() - baseline) / baseline;
            boolean isRegression = isLatencyMetric ? deviation > threshold : deviation < -threshold;

            if (isRegression) {
                regressions.add(new TrendResponse.Regression(
                        point.runId(), point.timestamp(), point.value(), baseline, deviation * 100));
            }
        }
        return regressions;
    }

    private Function<ReportSummary, Double> metricExtractor(String metric) {
        return switch (metric) {
            case "avgThroughputRecPerSec" -> ReportSummary::avgThroughputRecPerSec;
            case "peakThroughputRecPerSec" -> ReportSummary::peakThroughputRecPerSec;
            case "avgThroughputMBPerSec" -> ReportSummary::avgThroughputMBPerSec;
            case "avgLatencyMs" -> ReportSummary::avgLatencyMs;
            case "p50LatencyMs" -> ReportSummary::p50LatencyMs;
            case "p95LatencyMs" -> ReportSummary::p95LatencyMs;
            case "p99LatencyMs" -> ReportSummary::p99LatencyMs;
            case "p999LatencyMs" -> ReportSummary::p999LatencyMs;
            case "maxLatencyMs" -> ReportSummary::maxLatencyMs;
            case "errorRate" -> ReportSummary::errorRate;
            default -> null;
        };
    }
}
