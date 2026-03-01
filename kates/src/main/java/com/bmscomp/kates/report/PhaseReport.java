package com.bmscomp.kates.report;

import java.util.List;

import com.fasterxml.jackson.annotation.JsonInclude;

import com.bmscomp.kates.domain.MetricsSample;
import com.bmscomp.kates.domain.ScenarioPhase;
import com.bmscomp.kates.domain.SlaVerdict;

/**
 * Per-phase report within a {@link TestReport}.
 * Contains the phase metrics summary, SLA verdict, and time-series data.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public class PhaseReport {

    private String phaseName;
    private ScenarioPhase.PhaseType phaseType;
    private ReportSummary metrics;
    private SlaVerdict slaVerdict;
    private List<MetricsSample> timeSeries;

    public PhaseReport() {}

    public PhaseReport(
            String phaseName, ScenarioPhase.PhaseType phaseType, ReportSummary metrics, SlaVerdict slaVerdict) {
        this.phaseName = phaseName;
        this.phaseType = phaseType;
        this.metrics = metrics;
        this.slaVerdict = slaVerdict;
    }

    public String getPhaseName() {
        return phaseName;
    }

    public void setPhaseName(String phaseName) {
        this.phaseName = phaseName;
    }

    public ScenarioPhase.PhaseType getPhaseType() {
        return phaseType;
    }

    public void setPhaseType(ScenarioPhase.PhaseType phaseType) {
        this.phaseType = phaseType;
    }

    public ReportSummary getMetrics() {
        return metrics;
    }

    public void setMetrics(ReportSummary metrics) {
        this.metrics = metrics;
    }

    public SlaVerdict getSlaVerdict() {
        return slaVerdict;
    }

    public void setSlaVerdict(SlaVerdict slaVerdict) {
        this.slaVerdict = slaVerdict;
    }

    public List<MetricsSample> getTimeSeries() {
        return timeSeries;
    }

    public void setTimeSeries(List<MetricsSample> timeSeries) {
        this.timeSeries = timeSeries;
    }
}
