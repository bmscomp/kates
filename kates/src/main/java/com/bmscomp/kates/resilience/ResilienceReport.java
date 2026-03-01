package com.bmscomp.kates.resilience;

import java.time.Duration;
import java.util.List;
import java.util.Map;

import com.fasterxml.jackson.annotation.JsonInclude;

import com.bmscomp.kates.chaos.ChaosOutcome;
import com.bmscomp.kates.chaos.ProbeResult;
import com.bmscomp.kates.domain.IntegrityResult;
import com.bmscomp.kates.report.ReportSummary;
import com.bmscomp.kates.report.TestReport;

/**
 * Unified report for a resilience test: performance metrics + chaos outcome
 * + probe results + impact analysis + recovery time.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public class ResilienceReport {

    private TestReport performanceReport;
    private ChaosOutcome chaosOutcome;
    private ReportSummary preChaosSummary;
    private ReportSummary postChaosSummary;
    private Map<String, Double> impactDeltas;
    private String status;
    private IntegrityResult integrityResult;

    private List<ProbeResult> baselineProbes;
    private List<ProbeResult> duringChaosProbes;
    private List<ProbeResult> postRecoveryProbes;
    private Duration recoveryTime;

    public TestReport getPerformanceReport() {
        return performanceReport;
    }

    public void setPerformanceReport(TestReport performanceReport) {
        this.performanceReport = performanceReport;
    }

    public ChaosOutcome getChaosOutcome() {
        return chaosOutcome;
    }

    public void setChaosOutcome(ChaosOutcome chaosOutcome) {
        this.chaosOutcome = chaosOutcome;
    }

    public ReportSummary getPreChaosSummary() {
        return preChaosSummary;
    }

    public void setPreChaosSummary(ReportSummary preChaosSummary) {
        this.preChaosSummary = preChaosSummary;
    }

    public ReportSummary getPostChaosSummary() {
        return postChaosSummary;
    }

    public void setPostChaosSummary(ReportSummary postChaosSummary) {
        this.postChaosSummary = postChaosSummary;
    }

    public Map<String, Double> getImpactDeltas() {
        return impactDeltas;
    }

    public void setImpactDeltas(Map<String, Double> impactDeltas) {
        this.impactDeltas = impactDeltas;
    }

    public String getStatus() {
        return status;
    }

    public void setStatus(String status) {
        this.status = status;
    }

    public IntegrityResult getIntegrityResult() {
        return integrityResult;
    }

    public void setIntegrityResult(IntegrityResult integrityResult) {
        this.integrityResult = integrityResult;
    }

    public List<ProbeResult> getBaselineProbes() {
        return baselineProbes;
    }

    public void setBaselineProbes(List<ProbeResult> baselineProbes) {
        this.baselineProbes = baselineProbes;
    }

    public List<ProbeResult> getDuringChaosProbes() {
        return duringChaosProbes;
    }

    public void setDuringChaosProbes(List<ProbeResult> duringChaosProbes) {
        this.duringChaosProbes = duringChaosProbes;
    }

    public List<ProbeResult> getPostRecoveryProbes() {
        return postRecoveryProbes;
    }

    public void setPostRecoveryProbes(List<ProbeResult> postRecoveryProbes) {
        this.postRecoveryProbes = postRecoveryProbes;
    }

    public Duration getRecoveryTime() {
        return recoveryTime;
    }

    public void setRecoveryTime(Duration recoveryTime) {
        this.recoveryTime = recoveryTime;
    }
}
