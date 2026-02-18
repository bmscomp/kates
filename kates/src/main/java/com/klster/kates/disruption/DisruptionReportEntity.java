package com.klster.kates.disruption;

import java.time.Instant;
import jakarta.persistence.*;

/**
 * JPA entity for persisting disruption test reports.
 * The full report is stored as a JSON blob for flexibility.
 */
@Entity
@Table(name = "disruption_reports")
public class DisruptionReportEntity {

    @Id
    @Column(length = 36)
    private String id;

    @Column(name = "plan_name", nullable = false, length = 128)
    private String planName;

    @Column(nullable = false, length = 16)
    private String status;

    @Column(name = "sla_grade", length = 2)
    private String slaGrade;

    @Column(name = "created_at", nullable = false)
    private Instant createdAt;

    @Column(name = "report_json", nullable = false, columnDefinition = "TEXT")
    private String reportJson;

    @Column(name = "summary_json", columnDefinition = "TEXT")
    private String summaryJson;

    public DisruptionReportEntity() {}

    public DisruptionReportEntity(
            String id, String planName, String status, String slaGrade, String reportJson, String summaryJson) {
        this.id = id;
        this.planName = planName;
        this.status = status;
        this.slaGrade = slaGrade;
        this.createdAt = Instant.now();
        this.reportJson = reportJson;
        this.summaryJson = summaryJson;
    }

    public String getId() {
        return id;
    }

    public void setId(String id) {
        this.id = id;
    }

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

    public String getSlaGrade() {
        return slaGrade;
    }

    public void setSlaGrade(String slaGrade) {
        this.slaGrade = slaGrade;
    }

    public Instant getCreatedAt() {
        return createdAt;
    }

    public void setCreatedAt(Instant createdAt) {
        this.createdAt = createdAt;
    }

    public String getReportJson() {
        return reportJson;
    }

    public void setReportJson(String reportJson) {
        this.reportJson = reportJson;
    }

    public String getSummaryJson() {
        return summaryJson;
    }

    public void setSummaryJson(String summaryJson) {
        this.summaryJson = summaryJson;
    }
}
