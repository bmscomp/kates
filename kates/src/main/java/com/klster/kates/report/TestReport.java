package com.klster.kates.report;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.klster.kates.domain.SlaVerdict;
import com.klster.kates.domain.TestRun;

import java.util.List;
import java.util.Map;

/**
 * Complete structured report for a finished test run.
 * Aggregates overall metrics, per-phase breakdowns, SLA verdict, and metadata.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public class TestReport {

    private TestRun run;
    private ReportSummary summary;
    private List<PhaseReport> phases;
    private SlaVerdict overallSlaVerdict;
    private Map<String, String> metadata;
    private String generatedAt;

    public TestReport() {
    }

    public TestRun getRun() { return run; }
    public void setRun(TestRun run) { this.run = run; }

    public ReportSummary getSummary() { return summary; }
    public void setSummary(ReportSummary summary) { this.summary = summary; }

    public List<PhaseReport> getPhases() { return phases; }
    public void setPhases(List<PhaseReport> phases) { this.phases = phases; }

    public SlaVerdict getOverallSlaVerdict() { return overallSlaVerdict; }
    public void setOverallSlaVerdict(SlaVerdict overallSlaVerdict) { this.overallSlaVerdict = overallSlaVerdict; }

    public Map<String, String> getMetadata() { return metadata; }
    public void setMetadata(Map<String, String> metadata) { this.metadata = metadata; }

    public String getGeneratedAt() { return generatedAt; }
    public void setGeneratedAt(String generatedAt) { this.generatedAt = generatedAt; }
}
