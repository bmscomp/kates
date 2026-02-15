package com.klster.kates.resilience;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.klster.kates.chaos.ChaosOutcome;
import com.klster.kates.domain.IntegrityResult;
import com.klster.kates.report.ReportSummary;
import com.klster.kates.report.TestReport;

import java.util.Map;

/**
 * Unified report for a resilience test: performance metrics + chaos outcome + impact analysis.
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

    public TestReport getPerformanceReport() { return performanceReport; }
    public void setPerformanceReport(TestReport performanceReport) { this.performanceReport = performanceReport; }

    public ChaosOutcome getChaosOutcome() { return chaosOutcome; }
    public void setChaosOutcome(ChaosOutcome chaosOutcome) { this.chaosOutcome = chaosOutcome; }

    public ReportSummary getPreChaosSummary() { return preChaosSummary; }
    public void setPreChaosSummary(ReportSummary preChaosSummary) { this.preChaosSummary = preChaosSummary; }

    public ReportSummary getPostChaosSummary() { return postChaosSummary; }
    public void setPostChaosSummary(ReportSummary postChaosSummary) { this.postChaosSummary = postChaosSummary; }

    public Map<String, Double> getImpactDeltas() { return impactDeltas; }
    public void setImpactDeltas(Map<String, Double> impactDeltas) { this.impactDeltas = impactDeltas; }

    public String getStatus() { return status; }
    public void setStatus(String status) { this.status = status; }

    public IntegrityResult getIntegrityResult() { return integrityResult; }
    public void setIntegrityResult(IntegrityResult integrityResult) { this.integrityResult = integrityResult; }
}
