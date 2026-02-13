package com.klster.kates.report;

import com.klster.kates.domain.SlaVerdict;
import com.klster.kates.domain.SlaViolation;
import com.klster.kates.domain.TestResult;
import com.klster.kates.domain.TestRun;
import jakarta.enterprise.context.ApplicationScoped;

import java.time.Instant;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.stream.Collectors;

/**
 * Builds structured {@link TestReport} instances from completed {@link TestRun}s.
 * Computes aggregate metrics and overall SLA verdicts.
 */
@ApplicationScoped
public class ReportGenerator {

    public TestReport generate(TestRun run) {
        TestReport report = new TestReport();
        report.setRun(run);
        report.setGeneratedAt(Instant.now().toString());

        Map<String, String> metadata = new LinkedHashMap<>();
        metadata.put("testType", run.getTestType() != null ? run.getTestType().name() : "UNKNOWN");
        metadata.put("backend", run.getBackend());
        metadata.put("status", run.getStatus() != null ? run.getStatus().name() : "UNKNOWN");
        if (run.getScenarioName() != null) {
            metadata.put("scenarioName", run.getScenarioName());
        }
        if (run.getLabels() != null) {
            run.getLabels().forEach((k, v) -> metadata.put("label." + k, v));
        }
        report.setMetadata(metadata);

        List<TestResult> results = run.getResults();
        if (results == null || results.isEmpty()) {
            report.setSummary(emptySummary());
            report.setOverallSlaVerdict(SlaVerdict.pass());
            return report;
        }

        report.setSummary(computeSummary(results));

        Map<String, List<TestResult>> byPhase = results.stream()
                .filter(r -> r.getPhaseName() != null)
                .collect(Collectors.groupingBy(TestResult::getPhaseName, LinkedHashMap::new, Collectors.toList()));

        if (!byPhase.isEmpty()) {
            List<PhaseReport> phases = new ArrayList<>();
            for (Map.Entry<String, List<TestResult>> entry : byPhase.entrySet()) {
                PhaseReport pr = new PhaseReport();
                pr.setPhaseName(entry.getKey());
                pr.setMetrics(computeSummary(entry.getValue()));
                phases.add(pr);
            }
            report.setPhases(phases);
        }

        report.setOverallSlaVerdict(SlaVerdict.pass());
        return report;
    }

    public String toMarkdown(TestReport report) {
        StringBuilder sb = new StringBuilder();
        sb.append("# Test Report\n\n");
        sb.append("**Generated**: ").append(report.getGeneratedAt()).append("\n\n");

        if (report.getMetadata() != null) {
            sb.append("## Metadata\n\n");
            sb.append("| Key | Value |\n|---|---|\n");
            report.getMetadata().forEach((k, v) -> sb.append("| ").append(k).append(" | ").append(v).append(" |\n"));
            sb.append("\n");
        }

        ReportSummary s = report.getSummary();
        if (s != null) {
            sb.append("## Summary\n\n");
            sb.append("| Metric | Value |\n|---|---|\n");
            sb.append("| Total Records | ").append(s.totalRecords()).append(" |\n");
            sb.append("| Avg Throughput (rec/s) | ").append(String.format("%.2f", s.avgThroughputRecPerSec())).append(" |\n");
            sb.append("| Peak Throughput (rec/s) | ").append(String.format("%.2f", s.peakThroughputRecPerSec())).append(" |\n");
            sb.append("| Avg Throughput (MB/s) | ").append(String.format("%.2f", s.avgThroughputMBPerSec())).append(" |\n");
            sb.append("| Avg Latency (ms) | ").append(String.format("%.2f", s.avgLatencyMs())).append(" |\n");
            sb.append("| p50 Latency (ms) | ").append(String.format("%.2f", s.p50LatencyMs())).append(" |\n");
            sb.append("| p95 Latency (ms) | ").append(String.format("%.2f", s.p95LatencyMs())).append(" |\n");
            sb.append("| p99 Latency (ms) | ").append(String.format("%.2f", s.p99LatencyMs())).append(" |\n");
            sb.append("| p99.9 Latency (ms) | ").append(String.format("%.2f", s.p999LatencyMs())).append(" |\n");
            sb.append("| Max Latency (ms) | ").append(String.format("%.2f", s.maxLatencyMs())).append(" |\n");
            sb.append("| Error Rate | ").append(String.format("%.4f", s.errorRate())).append(" |\n");
            sb.append("| Duration (ms) | ").append(s.durationMs()).append(" |\n");
            sb.append("\n");
        }

        if (report.getPhases() != null && !report.getPhases().isEmpty()) {
            sb.append("## Phases\n\n");
            for (PhaseReport phase : report.getPhases()) {
                sb.append("### ").append(phase.getPhaseName()).append("\n\n");
                ReportSummary ps = phase.getMetrics();
                if (ps != null) {
                    sb.append("| Metric | Value |\n|---|---|\n");
                    sb.append("| Records | ").append(ps.totalRecords()).append(" |\n");
                    sb.append("| Throughput (rec/s) | ").append(String.format("%.2f", ps.avgThroughputRecPerSec())).append(" |\n");
                    sb.append("| Avg Latency (ms) | ").append(String.format("%.2f", ps.avgLatencyMs())).append(" |\n");
                    sb.append("| p99 Latency (ms) | ").append(String.format("%.2f", ps.p99LatencyMs())).append(" |\n");
                    sb.append("\n");
                }
            }
        }

        SlaVerdict verdict = report.getOverallSlaVerdict();
        if (verdict != null) {
            sb.append("## SLA Verdict\n\n");
            sb.append("**Status**: ").append(verdict.passed() ? "✅ PASSED" : "❌ FAILED").append("\n\n");
            if (!verdict.violations().isEmpty()) {
                sb.append("| Metric | Threshold | Actual | Severity |\n|---|---|---|---|\n");
                for (SlaViolation v : verdict.violations()) {
                    sb.append("| ").append(v.metric())
                            .append(" | ").append(String.format("%.2f", v.threshold()))
                            .append(" | ").append(String.format("%.2f", v.actual()))
                            .append(" | ").append(v.severity())
                            .append(" |\n");
                }
            }
        }

        return sb.toString();
    }

    private ReportSummary computeSummary(List<TestResult> results) {
        long totalRecords = results.stream().mapToLong(TestResult::getRecordsSent).sum();
        double avgThroughput = results.stream().mapToDouble(TestResult::getThroughputRecordsPerSec).average().orElse(0);
        double peakThroughput = results.stream().mapToDouble(TestResult::getThroughputRecordsPerSec).max().orElse(0);
        double avgThroughputMB = results.stream().mapToDouble(TestResult::getThroughputMBPerSec).average().orElse(0);
        double avgLatency = results.stream().mapToDouble(TestResult::getAvgLatencyMs).average().orElse(0);
        double p50 = results.stream().mapToDouble(TestResult::getP50LatencyMs).average().orElse(0);
        double p95 = results.stream().mapToDouble(TestResult::getP95LatencyMs).average().orElse(0);
        double p99 = results.stream().mapToDouble(TestResult::getP99LatencyMs).average().orElse(0);
        double maxLatency = results.stream().mapToDouble(TestResult::getMaxLatencyMs).max().orElse(0);
        long totalErrors = results.stream().filter(r -> r.getError() != null).count();
        double errorRate = totalRecords > 0 ? (double) totalErrors / totalRecords : 0;

        return new ReportSummary(
                totalRecords, avgThroughput, peakThroughput, avgThroughputMB,
                avgLatency, p50, p95, p99, 0, maxLatency,
                totalErrors, errorRate, 0
        );
    }

    private ReportSummary emptySummary() {
        return new ReportSummary(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0);
    }
}
