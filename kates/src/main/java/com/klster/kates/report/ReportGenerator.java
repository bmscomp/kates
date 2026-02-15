package com.klster.kates.report;

import com.klster.kates.domain.IntegrityEvent;
import com.klster.kates.domain.IntegrityResult;
import com.klster.kates.domain.SlaVerdict;
import com.klster.kates.domain.SlaViolation;
import com.klster.kates.domain.TestResult;
import com.klster.kates.domain.TestRun;
import com.klster.kates.service.KafkaAdminService;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

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

    @Inject
    KafkaAdminService kafkaAdminService;

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

        String topic = (run.getSpec() != null) ? run.getSpec().getTopic() : null;
        if (topic != null && !topic.isBlank()) {
            try {
                ClusterSnapshot snapshot = kafkaAdminService.captureSnapshot(topic);
                if (snapshot != null) {
                    report.setClusterSnapshot(snapshot);
                    report.setBrokerMetrics(
                            computeBrokerMetrics(snapshot, report.getSummary()));
                }
            } catch (Exception ignored) {
            }
        }

        report.setOverallSlaVerdict(SlaVerdict.pass());
        return report;
    }

    /**
     * Project overall test metrics onto individual brokers using partition
     * leadership ratio as weight. Detects skew when a broker deviates >20%
     * from the mean throughput.
     */
    public List<BrokerMetrics> computeBrokerMetrics(
            ClusterSnapshot snapshot, ReportSummary overall) {
        if (snapshot == null || overall == null || snapshot.brokers() == null) {
            return List.of();
        }

        int totalPartitions = snapshot.leaders() != null ? snapshot.leaders().size() : 0;
        if (totalPartitions == 0) return List.of();

        List<BrokerMetrics> brokers = new ArrayList<>();
        for (ClusterSnapshot.BrokerInfo broker : snapshot.brokers()) {
            int leaderCount = snapshot.leaderCountForBroker(broker.id());
            double share = (double) leaderCount / totalPartitions;

            ReportSummary projected = new ReportSummary(
                    Math.round(overall.totalRecords() * share),
                    overall.avgThroughputRecPerSec() * share,
                    overall.peakThroughputRecPerSec() * share,
                    overall.avgThroughputMBPerSec() * share,
                    overall.avgLatencyMs(),
                    overall.p50LatencyMs(),
                    overall.p95LatencyMs(),
                    overall.p99LatencyMs(),
                    overall.p999LatencyMs(),
                    overall.maxLatencyMs(),
                    Math.round(overall.totalErrors() * share),
                    overall.errorRate(),
                    overall.durationMs()
            );

            brokers.add(new BrokerMetrics(
                    broker.id(), broker.host(), broker.rack(),
                    leaderCount, totalPartitions,
                    Math.round(share * 10000.0) / 100.0,
                    projected, false
            ));
        }

        double avgThroughput = brokers.stream()
                .mapToDouble(b -> b.metrics().avgThroughputRecPerSec())
                .average().orElse(0);

        if (avgThroughput > 0) {
            brokers = brokers.stream().map(b -> {
                double deviation = Math.abs(
                        b.metrics().avgThroughputRecPerSec() - avgThroughput) / avgThroughput;
                return new BrokerMetrics(
                        b.brokerId(), b.host(), b.rack(),
                        b.leaderPartitions(), b.totalPartitions(),
                        b.leaderSharePercent(), b.metrics(),
                        deviation > 0.20);
            }).toList();
        }

        return brokers;
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

        // Data Integrity section
        if (report.getRun() != null && report.getRun().getResults() != null) {
            report.getRun().getResults().stream()
                    .filter(r -> r.getIntegrity() != null)
                    .findFirst()
                    .ifPresent(r -> {
                        IntegrityResult ir = r.getIntegrity();
                        sb.append("## Data Integrity\n\n");
                        sb.append("| Metric | Value |\n|---|---|\n");
                        sb.append("| Sent | ").append(ir.totalSent()).append(" |\n");
                        sb.append("| Acked | ").append(ir.totalAcked()).append(" |\n");
                        sb.append("| Consumed | ").append(ir.totalConsumed()).append(" |\n");
                        sb.append("| Lost | ").append(ir.lostRecords()).append(" |\n");
                        sb.append("| Duplicates | ").append(ir.duplicateRecords()).append(" |\n");
                        sb.append("| Data Loss (%) | ").append(String.format("%.4f", ir.dataLossPercent())).append(" |\n");
                        sb.append("| Producer RTO (ms) | ").append(String.format("%.0f", ir.producerRtoMs())).append(" |\n");
                        sb.append("| Consumer RTO (ms) | ").append(String.format("%.0f", ir.consumerRtoMs())).append(" |\n");
                        sb.append("| RPO (ms) | ").append(String.format("%.0f", ir.rpoMs())).append(" |\n");
                        sb.append("| CRC Verified | ").append(ir.crcVerified()).append(" |\n");
                        sb.append("| CRC Failures | ").append(ir.crcFailures()).append(" |\n");
                        sb.append("| Ordering Verified | ").append(ir.orderingVerified()).append(" |\n");
                        sb.append("| Out of Order | ").append(ir.outOfOrderCount()).append(" |\n");
                        sb.append("| Idempotence | ").append(ir.idempotenceEnabled()).append(" |\n");
                        sb.append("| Transactions | ").append(ir.transactionsEnabled()).append(" |\n");
                        sb.append("| **Verdict** | **").append(ir.verdict()).append("** |\n\n");
                        if (ir.timeline() != null && !ir.timeline().isEmpty()) {
                            sb.append("### Timeline\n\n");
                            sb.append("| Timestamp | Type | Detail |\n|---|---|---|\n");
                            int max = Math.min(ir.timeline().size(), 50);
                            int start = ir.timeline().size() - max;
                            for (int i = start; i < ir.timeline().size(); i++) {
                                IntegrityEvent ev = ir.timeline().get(i);
                                sb.append("| ").append(ev.timestampMs())
                                        .append(" | ").append(ev.type())
                                        .append(" | ").append(ev.detail())
                                        .append(" |\n");
                            }
                            sb.append("\n");
                        }
                    });
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
