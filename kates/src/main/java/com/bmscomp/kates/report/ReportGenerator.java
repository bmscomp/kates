package com.bmscomp.kates.report;

import java.time.Instant;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.stream.Collectors;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import com.bmscomp.kates.domain.IntegrityEvent;
import com.bmscomp.kates.domain.IntegrityResult;
import com.bmscomp.kates.domain.SlaVerdict;
import com.bmscomp.kates.domain.SlaViolation;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.engine.KatesMetrics;
import com.bmscomp.kates.service.ClusterHealthService;
import com.bmscomp.kates.util.MetricUtils;

/**
 * Builds structured {@link TestReport} instances from completed {@link TestRun}s.
 * Computes aggregate metrics and overall SLA verdicts.
 * Reports for terminal runs (DONE/FAILED) that carry their results are cached
 * to avoid redundant recomputation.
 */
@ApplicationScoped
public class ReportGenerator {

    @Inject
    ClusterHealthService clusterHealthService;

    @Inject
    KatesMetrics katesMetrics;

    @Inject
    com.bmscomp.kates.engine.SlaEvaluator slaEvaluator;

    /**
     * Evaluates the run's SLA against {@code rows} — the whole run or one
     * phase — summarized as {@code summary}, but only once the run is terminal.
     *
     * <p>A report can be generated while a run is still in flight, and partial
     * metrics routinely sit below their target mid-ramp — grading those would
     * report a breach the finished run does not have. In-flight runs therefore
     * carry a passing verdict until there is a complete result to judge, and
     * their JUnit export is refused (ReportResource) rather than read as green.
     */
    private SlaVerdict evaluateSla(TestRun run, List<TestResult> rows, ReportSummary summary) {
        if (!isTerminal(run.getStatus())) {
            return SlaVerdict.pass();
        }
        return slaEvaluator.evaluate(run.getSla(), toSlaMetrics(run, rows, summary));
    }

    /**
     * The run's own verdict: its status, then its SLA. A FAILED run fails
     * whether or not it declared an SLA, with a first violation naming the
     * failure. A run that failed creating its topic has no rows to breach
     * anything, and a latency-only SLA never looks at how the run ended, so
     * both used to pass, and the JUnit suites exported from them with them.
     * Phases are judged on their SLA alone: the status belongs to the run.
     */
    private SlaVerdict overallVerdict(TestRun run, List<TestResult> rows, ReportSummary summary) {
        SlaVerdict verdict = evaluateSla(run, rows, summary);
        if (run.getStatus() != TestResult.TaskStatus.FAILED) {
            return verdict;
        }
        List<SlaViolation> violations = new ArrayList<>();
        violations.add(SlaViolation.runFailed(failureOf(run)));
        violations.addAll(verdict.violations());
        return SlaVerdict.fail(violations);
    }

    /**
     * What a FAILED run's status violation says: the first task error, or that
     * no task ran. A run that failed before submitting its tasks, creating its
     * topic for instance, stores no row and no reason; only the log has it.
     */
    private static String failureOf(TestRun run) {
        List<TestResult> results = run.getResults();
        if (results == null || results.isEmpty()) {
            return "FAILED before any task ran";
        }
        return results.stream()
                .map(TestResult::getError)
                .filter(error -> error != null && !error.isBlank())
                .findFirst()
                .map(error -> "FAILED: " + error)
                .orElse("FAILED");
    }

    /**
     * Maps an aggregated summary onto the values an SLA is evaluated against.
     *
     * <p>A latency is -1 when no row measured one, so its gate fails as not
     * measured instead of passing against the summary's 0. P99.9 always is:
     * a task row keeps no P99.9, so the summary's is always 0.
     *
     * <p>Resilience values come from the run's integrity results rather than the
     * summary, which does not carry them; the worst value across tasks is used,
     * and -1 means the run had no integrity check so the corresponding
     * constraint is skipped instead of silently passing.
     */
    private static com.bmscomp.kates.domain.SlaMetrics toSlaMetrics(
            TestRun run, List<TestResult> rows, ReportSummary summary) {
        double dataLossPercent = -1;
        double maxRtoMs = -1;
        double rpoMs = -1;

        for (TestResult result : run.getResults()) {
            com.bmscomp.kates.domain.IntegrityResult integrity = result.getIntegrity();
            if (integrity == null) {
                continue;
            }
            dataLossPercent = Math.max(dataLossPercent, integrity.dataLossPercent());
            maxRtoMs = Math.max(maxRtoMs, integrity.maxRtoMs());
            rpoMs = Math.max(rpoMs, integrity.rpoMs());
        }

        boolean latencyMeasured = MetricUtils.measuredLatency(rows);
        return new com.bmscomp.kates.domain.SlaMetrics(
                latencyMeasured ? summary.p99LatencyMs() : -1,
                -1,
                latencyMeasured ? summary.avgLatencyMs() : -1,
                summary.avgThroughputRecPerSec(),
                summary.totalRecords(),
                summary.errorRate(),
                dataLossPercent,
                maxRtoMs,
                rpoMs);
    }

    /**
     * Reports embed histograms and timelines; an uncapped cache grows without
     * bound in a long-running service. LRU-evict beyond MAX_CACHED_REPORTS —
     * evicted reports are simply regenerated on next access.
     */
    private static final int MAX_CACHED_REPORTS = 200;

    private final Map<String, TestReport> reportCache =
            java.util.Collections.synchronizedMap(new LinkedHashMap<>(64, 0.75f, true) {
                @Override
                protected boolean removeEldestEntry(Map.Entry<String, TestReport> eldest) {
                    return size() > MAX_CACHED_REPORTS;
                }
            });

    /**
     * Evict a cached report, forcing regeneration on the next call.
     */
    public void evictReport(String runId) {
        reportCache.remove(runId);
    }

    public TestReport generate(TestRun run) {
        boolean cacheable = isCacheable(run);
        if (cacheable) {
            TestReport cached = reportCache.get(run.getId());
            if (cached != null) {
                return cached;
            }
        }

        TestReport report = doGenerate(run);

        if (cacheable) {
            reportCache.put(run.getId(), report);
        }

        return report;
    }

    /**
     * Only a finished run that carries its results has its report cached.
     *
     * <p>A run read by a list query has no results, and its report is all
     * zeros. Cached under the run's id, that report answered for the run until
     * it was evicted — the report, its JUnit export (tests="0" failures="0"),
     * compare and the regression check — and a trend did this to every run in
     * its window. A run without results is cheap to report again: its report
     * stops before the cluster snapshot.
     */
    private boolean isCacheable(TestRun run) {
        return run.getId() != null
                && isTerminal(run.getStatus())
                && run.getResults() != null
                && !run.getResults().isEmpty();
    }

    private boolean isTerminal(TestResult.TaskStatus status) {
        return status == TestResult.TaskStatus.DONE || status == TestResult.TaskStatus.FAILED;
    }

    private TestReport doGenerate(TestRun run) {
        TestReport report = new TestReport();
        report.setRun(run);
        report.setGeneratedAt(Instant.now().toString());

        Map<String, String> metadata = new LinkedHashMap<>();
        // First, and not optional: the exported formats carry no other copy of
        // the run id. A markdown or CSV report saved to a file said which type
        // of test it was and how it did, but not WHICH run produced it — so two
        // reports from the same suite were indistinguishable once downloaded.
        if (run.getId() != null) {
            metadata.put("runId", run.getId());
        }
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
            ReportSummary empty = MetricUtils.computeSummary(List.of());
            report.setSummary(empty);
            // A terminal run that produced nothing still fails a
            // minRecordsProcessed / minThroughput SLA — that IS the breach —
            // and a latency gate, which nothing measured. A FAILED one, such as
            // a run whose topic could not be created, fails without an SLA.
            report.setOverallSlaVerdict(overallVerdict(run, List.of(), empty));
            return report;
        }

        report.setSummary(MetricUtils.computeSummary(results));

        Map<String, ReportSummary> byPhase = phaseSummaries(results);

        if (!byPhase.isEmpty()) {
            // A phase's verdict also needs its rows, to tell an unmeasured
            // latency from a measured 0.
            Map<String, List<TestResult>> phaseRows = phaseResults(results);
            List<PhaseReport> phases = new ArrayList<>();
            for (Map.Entry<String, ReportSummary> entry : byPhase.entrySet()) {
                PhaseReport pr = new PhaseReport();
                pr.setPhaseName(entry.getKey());
                pr.setMetrics(entry.getValue());
                pr.setSlaVerdict(evaluateSla(run, phaseRows.get(entry.getKey()), entry.getValue()));
                phases.add(pr);
            }
            report.setPhases(phases);
        }

        String topic = (run.getSpec() != null) ? run.getSpec().getTopic() : null;
        if (topic != null && !topic.isBlank()) {
            try {
                ClusterSnapshot snapshot = clusterHealthService.captureSnapshot(topic);
                if (snapshot != null) {
                    report.setClusterSnapshot(snapshot);
                    report.setBrokerMetrics(computeBrokerMetrics(snapshot, report.getSummary()));
                }
            } catch (Exception ignored) {
            }
        }

        // Actually evaluate the run's SLA. This was hardcoded to pass(), so a
        // declared SLA was stored, rendered and exported as PASSED no matter what
        // the run did — every gate built on it (report verdict, JUnit export,
        // --fail-on-sla-breach) was green by construction. Runs without an SLA
        // still pass, unless they FAILED: SlaEvaluator returns pass() for a
        // null/empty definition.
        report.setOverallSlaVerdict(overallVerdict(run, results, report.getSummary()));

        String typeName = run.getTestType() != null ? run.getTestType().name() : "UNKNOWN";
        katesMetrics.recordSlaEvaluation(typeName, report.getOverallSlaVerdict().passed());

        return report;
    }

    /**
     * Each phase's summary, by phase name in the order the phases first appear
     * among the results; a result without a phase belongs to none. Reports and
     * trends both read phases through this, so a trend's phase point is the
     * phase summary of its run's report.
     */
    public static Map<String, ReportSummary> phaseSummaries(List<TestResult> results) {
        Map<String, ReportSummary> summaries = new LinkedHashMap<>();
        phaseResults(results).forEach((phase, rows) -> summaries.put(phase, MetricUtils.computeSummary(rows)));
        return summaries;
    }

    /** Each phase's results, grouped as {@link #phaseSummaries} groups them. */
    static Map<String, List<TestResult>> phaseResults(List<TestResult> results) {
        return results.stream()
                .filter(r -> r.getPhaseName() != null)
                .collect(Collectors.groupingBy(TestResult::getPhaseName, LinkedHashMap::new, Collectors.toList()));
    }

    /**
     * Project overall test metrics onto individual brokers using partition
     * leadership ratio as weight. Detects skew when a broker deviates >20%
     * from the mean throughput.
     */
    List<BrokerMetrics> computeBrokerMetrics(ClusterSnapshot snapshot, ReportSummary overall) {
        if (snapshot == null || overall == null || snapshot.brokers() == null) {
            return List.of();
        }

        int totalPartitions = snapshot.leaders() != null ? snapshot.leaders().size() : 0;
        if (totalPartitions == 0) return List.of();

        List<BrokerMetrics> brokers = new ArrayList<>();
        for (ClusterSnapshot.BrokerInfo broker : snapshot.brokers()) {
            int leaderCount = snapshot.leaderCountForBroker(broker.id());
            int replicaCount = snapshot.replicaCountForBroker(broker.id());
            int isrCount = snapshot.isrCountForBroker(broker.id());
            int underReplicated = snapshot.underReplicatedCountForBroker(broker.id());
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
                    overall.durationMs());

            brokers.add(new BrokerMetrics(
                    broker.id(),
                    broker.host(),
                    broker.rack(),
                    broker.id() == snapshot.controllerId(),
                    leaderCount,
                    replicaCount,
                    isrCount,
                    underReplicated,
                    totalPartitions,
                    Math.round(share * 10000.0) / 100.0,
                    0.0,
                    false,
                    projected));
        }

        double avgThroughput = brokers.stream()
                .mapToDouble(b -> b.metrics().avgThroughputRecPerSec())
                .average()
                .orElse(0);

        if (avgThroughput > 0) {
            brokers = brokers.stream()
                    .map(b -> {
                        double deviation = (b.metrics().avgThroughputRecPerSec() - avgThroughput) / avgThroughput;
                        double deviationPct = Math.round(deviation * 10000.0) / 100.0;
                        return new BrokerMetrics(
                                b.brokerId(),
                                b.host(),
                                b.rack(),
                                b.isController(),
                                b.leaderPartitions(),
                                b.replicaPartitions(),
                                b.isrPartitions(),
                                b.underReplicatedPartitions(),
                                b.totalPartitions(),
                                b.leaderSharePercent(),
                                deviationPct,
                                Math.abs(deviation) > 0.20,
                                b.metrics());
                    })
                    .toList();
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
            report.getMetadata()
                    .forEach((k, v) ->
                            sb.append("| ").append(k).append(" | ").append(v).append(" |\n"));
            sb.append("\n");
        }

        ReportSummary s = report.getSummary();
        if (s != null) {
            sb.append("## Summary\n\n");
            sb.append("| Metric | Value |\n|---|---|\n");
            sb.append("| Total Records | ").append(s.totalRecords()).append(" |\n");
            sb.append("| Avg Throughput (rec/s) | ")
                    .append(String.format("%.2f", s.avgThroughputRecPerSec()))
                    .append(" |\n");
            sb.append("| Peak Throughput (rec/s) | ")
                    .append(String.format("%.2f", s.peakThroughputRecPerSec()))
                    .append(" |\n");
            sb.append("| Avg Throughput (MB/s) | ")
                    .append(String.format("%.2f", s.avgThroughputMBPerSec()))
                    .append(" |\n");
            sb.append("| Avg Latency (ms) | ")
                    .append(String.format("%.2f", s.avgLatencyMs()))
                    .append(" |\n");
            sb.append("| p50 Latency (ms) | ")
                    .append(String.format("%.2f", s.p50LatencyMs()))
                    .append(" |\n");
            sb.append("| p95 Latency (ms) | ")
                    .append(String.format("%.2f", s.p95LatencyMs()))
                    .append(" |\n");
            sb.append("| p99 Latency (ms) | ")
                    .append(String.format("%.2f", s.p99LatencyMs()))
                    .append(" |\n");
            sb.append("| p99.9 Latency (ms) | ")
                    .append(String.format("%.2f", s.p999LatencyMs()))
                    .append(" |\n");
            sb.append("| Max Latency (ms) | ")
                    .append(String.format("%.2f", s.maxLatencyMs()))
                    .append(" |\n");
            sb.append("| Error Rate | ")
                    .append(String.format("%.4f", s.errorRate()))
                    .append(" |\n");
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
                    sb.append("| Throughput (rec/s) | ")
                            .append(String.format("%.2f", ps.avgThroughputRecPerSec()))
                            .append(" |\n");
                    sb.append("| Avg Latency (ms) | ")
                            .append(String.format("%.2f", ps.avgLatencyMs()))
                            .append(" |\n");
                    sb.append("| p99 Latency (ms) | ")
                            .append(String.format("%.2f", ps.p99LatencyMs()))
                            .append(" |\n");
                    sb.append("\n");
                }
            }
        }

        SlaVerdict verdict = report.getOverallSlaVerdict();
        if (verdict != null) {
            sb.append("## SLA Verdict\n\n");
            sb.append("**Status**: ")
                    .append(verdict.passed() ? "✅ PASSED" : "❌ FAILED")
                    .append("\n\n");
            if (!verdict.violations().isEmpty()) {
                sb.append("| Metric | Threshold | Actual | Severity |\n|---|---|---|---|\n");
                for (SlaViolation v : verdict.violations()) {
                    // -1 is no value: a FAILED run's status has no threshold,
                    // and the reason stands in for an actual value the run did
                    // not measure or that a status cannot have.
                    sb.append("| ")
                            .append(v.metric())
                            .append(" | ")
                            .append(v.threshold() >= 0 ? String.format("%.2f", v.threshold()) : "—")
                            .append(" | ")
                            .append(v.reason() != null ? tableCell(v.reason()) : String.format("%.2f", v.actual()))
                            .append(" | ")
                            .append(v.severity())
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
                        sb.append("| Duplicates | ")
                                .append(ir.duplicateRecords())
                                .append(" |\n");
                        sb.append("| Data Loss (%) | ")
                                .append(String.format("%.4f", ir.dataLossPercent()))
                                .append(" |\n");
                        sb.append("| Producer RTO (ms) | ")
                                .append(String.format("%.0f", ir.producerRtoMs()))
                                .append(" |\n");
                        sb.append("| Consumer RTO (ms) | ")
                                .append(String.format("%.0f", ir.consumerRtoMs()))
                                .append(" |\n");
                        sb.append("| RPO (ms) | ")
                                .append(ir.rpo() != null ? String.format("%.0f", ir.rpoMs()) : "not measured")
                                .append(" |\n");
                        sb.append("| CRC Verified | ").append(ir.crcVerified()).append(" |\n");
                        sb.append("| CRC Failures | ").append(ir.crcFailures()).append(" |\n");
                        sb.append("| Ordering Verified | ")
                                .append(ir.orderingVerified())
                                .append(" |\n");
                        sb.append("| Out of Order | ")
                                .append(ir.outOfOrderCount())
                                .append(" |\n");
                        sb.append("| Idempotence | ")
                                .append(ir.idempotenceEnabled())
                                .append(" |\n");
                        sb.append("| Transactions | ")
                                .append(ir.transactionsEnabled())
                                .append(" |\n");
                        sb.append("| **Verdict** | **").append(ir.verdict()).append("** |\n\n");
                        if (ir.timeline() != null && !ir.timeline().isEmpty()) {
                            sb.append("### Timeline\n\n");
                            sb.append("| Timestamp | Type | Detail |\n|---|---|---|\n");
                            int max = Math.min(ir.timeline().size(), 50);
                            int start = ir.timeline().size() - max;
                            for (int i = start; i < ir.timeline().size(); i++) {
                                IntegrityEvent ev = ir.timeline().get(i);
                                sb.append("| ")
                                        .append(ev.timestampMs())
                                        .append(" | ")
                                        .append(ev.type())
                                        .append(" | ")
                                        .append(ev.detail())
                                        .append(" |\n");
                            }
                            sb.append("\n");
                        }
                    });
        }

        return sb.toString();
    }

    /** A task error as one Markdown table cell: a pipe or a line break would end the row. */
    private static String tableCell(String text) {
        return text.replace("|", "\\|").replace('\n', ' ').replace('\r', ' ');
    }
}
