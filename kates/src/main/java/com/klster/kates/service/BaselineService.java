package com.klster.kates.service;

import java.time.Instant;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.ArrayList;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;

import com.klster.kates.domain.TestRun;
import com.klster.kates.domain.TestType;
import com.klster.kates.persistence.BaselineEntity;
import com.klster.kates.report.ReportGenerator;
import com.klster.kates.report.ReportSummary;
import com.klster.kates.report.TestReport;

@ApplicationScoped
public class BaselineService {

    @Inject
    EntityManager em;

    @Inject
    TestRunRepository repository;

    @Inject
    ReportGenerator reportGenerator;

    @Transactional
    public BaselineEntity set(TestType type, String runId) {
        BaselineEntity existing = em.find(BaselineEntity.class, type);
        if (existing != null) {
            existing.setRunId(runId);
            existing.setSetAt(Instant.now());
            return em.merge(existing);
        }
        BaselineEntity entity = new BaselineEntity(type, runId);
        em.persist(entity);
        return entity;
    }

    @Transactional
    public boolean unset(TestType type) {
        BaselineEntity entity = em.find(BaselineEntity.class, type);
        if (entity != null) {
            em.remove(entity);
            return true;
        }
        return false;
    }

    public Optional<BaselineEntity> get(TestType type) {
        return Optional.ofNullable(em.find(BaselineEntity.class, type));
    }

    public List<BaselineEntity> listAll() {
        return em.createQuery("SELECT b FROM BaselineEntity b ORDER BY b.testType", BaselineEntity.class)
                .getResultList();
    }

    public Map<String, Object> compareRegression(String runId) {
        Optional<TestRun> runOpt = repository.findById(runId);
        if (runOpt.isEmpty()) {
            return null;
        }
        TestRun run = runOpt.get();
        Optional<BaselineEntity> baselineOpt = get(run.getTestType());
        if (baselineOpt.isEmpty()) {
            return Map.of("error", "No baseline set for type: " + run.getTestType());
        }
        BaselineEntity baseline = baselineOpt.get();
        Optional<TestRun> baselineRunOpt = repository.findById(baseline.getRunId());
        if (baselineRunOpt.isEmpty()) {
            return Map.of("error", "Baseline run not found: " + baseline.getRunId());
        }

        TestReport currentReport = reportGenerator.generate(run);
        TestReport baselineReport = reportGenerator.generate(baselineRunOpt.get());
        ReportSummary currentSummary = currentReport.getSummary();
        ReportSummary baselineSummary = baselineReport.getSummary();

        if (currentSummary == null || baselineSummary == null) {
            return Map.of("error", "Unable to generate report summaries");
        }

        Map<String, Object> result = new LinkedHashMap<>();
        result.put("runId", runId);
        result.put("baselineId", baseline.getRunId());
        result.put("testType", run.getTestType().name());

        Map<String, Map<String, Object>> deltas = new LinkedHashMap<>();
        deltas.put("avgThroughputRecPerSec", buildDelta(
                baselineSummary.avgThroughputRecPerSec(),
                currentSummary.avgThroughputRecPerSec()));
        deltas.put("peakThroughputRecPerSec", buildDelta(
                baselineSummary.peakThroughputRecPerSec(),
                currentSummary.peakThroughputRecPerSec()));
        deltas.put("avgLatencyMs", buildDelta(
                baselineSummary.avgLatencyMs(),
                currentSummary.avgLatencyMs()));
        deltas.put("p50LatencyMs", buildDelta(
                baselineSummary.p50LatencyMs(),
                currentSummary.p50LatencyMs()));
        deltas.put("p95LatencyMs", buildDelta(
                baselineSummary.p95LatencyMs(),
                currentSummary.p95LatencyMs()));
        deltas.put("p99LatencyMs", buildDelta(
                baselineSummary.p99LatencyMs(),
                currentSummary.p99LatencyMs()));
        deltas.put("maxLatencyMs", buildDelta(
                baselineSummary.maxLatencyMs(),
                currentSummary.maxLatencyMs()));
        deltas.put("errorRate", buildDelta(
                baselineSummary.errorRate(),
                currentSummary.errorRate()));
        result.put("deltas", deltas);

        boolean regressionDetected =
                detectThroughputDrop(baselineSummary.avgThroughputRecPerSec(),
                        currentSummary.avgThroughputRecPerSec(), 0.10) ||
                detectLatencySpike(baselineSummary.p99LatencyMs(),
                        currentSummary.p99LatencyMs(), 0.20) ||
                (currentSummary.errorRate() > baselineSummary.errorRate() + 0.001);

        result.put("regressionDetected", regressionDetected);

        List<String> warnings = new ArrayList<>();
        if (detectThroughputDrop(baselineSummary.avgThroughputRecPerSec(),
                currentSummary.avgThroughputRecPerSec(), 0.10)) {
            warnings.add("Throughput dropped > 10%");
        }
        if (detectLatencySpike(baselineSummary.p99LatencyMs(),
                currentSummary.p99LatencyMs(), 0.20)) {
            warnings.add("P99 latency increased > 20%");
        }
        if (currentSummary.errorRate() > baselineSummary.errorRate() + 0.001) {
            warnings.add("Error rate increased");
        }
        if (!warnings.isEmpty()) {
            result.put("warnings", warnings);
        }

        return result;
    }

    private Map<String, Object> buildDelta(double baseline, double current) {
        Map<String, Object> delta = new LinkedHashMap<>();
        delta.put("baseline", baseline);
        delta.put("current", current);
        if (baseline != 0) {
            double pct = ((current - baseline) / baseline) * 100.0;
            delta.put("delta", Math.round(pct * 10.0) / 10.0);
        }
        return delta;
    }

    private boolean detectThroughputDrop(double baseline, double current, double threshold) {
        if (baseline == 0) return false;
        double change = (current - baseline) / baseline;
        return change < -threshold;
    }

    private boolean detectLatencySpike(double baseline, double current, double threshold) {
        if (baseline == 0) return false;
        double change = (current - baseline) / baseline;
        return change > threshold;
    }
}
