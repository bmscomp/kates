package com.klster.kates.trend;

import com.klster.kates.domain.TestRun;
import com.klster.kates.domain.TestType;
import com.klster.kates.report.ReportGenerator;
import com.klster.kates.report.ReportSummary;
import com.klster.kates.report.TestReport;
import com.klster.kates.service.TestRunRepository;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.ArrayList;
import java.util.List;
import java.util.function.Function;

/**
 * Computes historical metric trends from completed test runs.
 * Supports rolling baselines and automatic regression detection.
 */
@ApplicationScoped
public class TrendService {

    @Inject
    TestRunRepository repository;

    @Inject
    ReportGenerator reportGenerator;

    public TrendResponse computeTrend(TestType type, String metric, int days, int baselineWindow) {
        Instant from = Instant.now().minus(days, ChronoUnit.DAYS);
        Instant to = Instant.now();

        List<TestRun> runs = repository.findByTypeAndDateRange(type, from, to);

        Function<ReportSummary, Double> extractor = metricExtractor(metric);
        if (extractor == null) {
            return TrendResponse.empty(type.name(), metric);
        }

        List<TrendResponse.DataPoint> dataPoints = new ArrayList<>();
        for (TestRun run : runs) {
            TestReport report = reportGenerator.generate(run);
            ReportSummary summary = report.getSummary();
            if (summary != null) {
                double value = extractor.apply(summary);
                dataPoints.add(new TrendResponse.DataPoint(run.getCreatedAt(), run.getId(), value));
            }
        }

        double baseline = computeBaseline(dataPoints, baselineWindow);
        List<TrendResponse.Regression> regressions = detectRegressions(dataPoints, baseline, metric);

        return new TrendResponse(type.name(), metric, dataPoints, baseline, regressions);
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
        double threshold = 0.20; // 20% degradation

        List<TrendResponse.Regression> regressions = new ArrayList<>();
        for (TrendResponse.DataPoint point : points) {
            double deviation = (point.value() - baseline) / baseline;

            // For latency metrics, increase = regression. For throughput, decrease = regression.
            boolean isRegression = isLatencyMetric ? deviation > threshold : deviation < -threshold;

            if (isRegression) {
                regressions.add(new TrendResponse.Regression(
                        point.runId(), point.timestamp(), point.value(),
                        baseline, deviation * 100));
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
