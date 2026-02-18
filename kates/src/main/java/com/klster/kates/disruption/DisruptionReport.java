package com.klster.kates.disruption;

import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

import com.fasterxml.jackson.annotation.JsonInclude;

import com.klster.kates.chaos.ChaosOutcome;
import com.klster.kates.chaos.DisruptionType;
import com.klster.kates.chaos.K8sPodWatcher;
import com.klster.kates.report.ReportSummary;
import com.klster.kates.resilience.ResilienceReport;

/**
 * Unified report for a multi-step disruption test.
 * Contains per-step results with pod timelines, recovery metrics,
 * Kafka intelligence, Prometheus metrics, SLA grading, safety validation, and rollback info.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public class DisruptionReport {

    private String planName;
    private String status;
    private List<StepReport> stepReports = new ArrayList<>();
    private DisruptionSummary summary;
    private ResilienceReport resilienceReport;
    private List<String> validationWarnings;
    private SlaGrader.SlaVerdict slaVerdict;

    public record StepReport(
            String stepName,
            DisruptionType disruptionType,
            ChaosOutcome chaosOutcome,
            List<K8sPodWatcher.PodEvent> podTimeline,
            Duration timeToFirstReady,
            Duration timeToAllReady,
            Duration strimziRecoveryTime,
            ReportSummary preDisruptionMetrics,
            ReportSummary postDisruptionMetrics,
            Map<String, Double> impactDeltas,
            Integer targetedLeaderBrokerId,
            IsrSnapshot.Metrics isrMetrics,
            LagSnapshot.Metrics lagMetrics,
            boolean rolledBack,
            String rollbackReason) {}

    public record DisruptionSummary(
            int totalSteps,
            int passedSteps,
            Duration worstRecovery,
            double avgThroughputDegradation,
            double maxP99LatencySpike,
            boolean slaViolated,
            Duration worstIsrRecovery,
            long peakConsumerLag) {}

    public String getPlanName() {
        return planName;
    }

    public void setPlanName(String planName) {
        this.planName = planName;
    }

    public String getStatus() {
        return status;
    }

    public void setStatus(String status) {
        this.status = status;
    }

    public List<StepReport> getStepReports() {
        return stepReports;
    }

    public void setStepReports(List<StepReport> stepReports) {
        this.stepReports = stepReports;
    }

    public DisruptionSummary getSummary() {
        return summary;
    }

    public void setSummary(DisruptionSummary summary) {
        this.summary = summary;
    }

    public ResilienceReport getResilienceReport() {
        return resilienceReport;
    }

    public void setResilienceReport(ResilienceReport resilienceReport) {
        this.resilienceReport = resilienceReport;
    }

    public List<String> getValidationWarnings() {
        return validationWarnings;
    }

    public void setValidationWarnings(List<String> validationWarnings) {
        this.validationWarnings = validationWarnings;
    }

    public SlaGrader.SlaVerdict getSlaVerdict() {
        return slaVerdict;
    }

    public void setSlaVerdict(SlaGrader.SlaVerdict slaVerdict) {
        this.slaVerdict = slaVerdict;
    }
}
