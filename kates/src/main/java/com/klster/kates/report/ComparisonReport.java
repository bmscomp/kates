package com.klster.kates.report;

import java.util.List;
import java.util.Map;

import com.fasterxml.jackson.annotation.JsonInclude;

/**
 * Side-by-side comparison of multiple test runs.
 * Contains per-run summaries and delta percentages relative to a baseline.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public class ComparisonReport {

    private String baselineRunId;
    private List<ComparisonEntry> runs;
    private Map<String, Double> deltas;

    public ComparisonReport() {}

    public String getBaselineRunId() {
        return baselineRunId;
    }

    public void setBaselineRunId(String baselineRunId) {
        this.baselineRunId = baselineRunId;
    }

    public List<ComparisonEntry> getRuns() {
        return runs;
    }

    public void setRuns(List<ComparisonEntry> runs) {
        this.runs = runs;
    }

    public Map<String, Double> getDeltas() {
        return deltas;
    }

    public void setDeltas(Map<String, Double> deltas) {
        this.deltas = deltas;
    }

    public record ComparisonEntry(
            String runId, String scenarioName, String testType, String backend, ReportSummary summary) {}
}
