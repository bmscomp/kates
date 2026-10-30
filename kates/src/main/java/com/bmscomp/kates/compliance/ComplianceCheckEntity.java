package com.bmscomp.kates.compliance;

import java.time.Instant;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.Id;
import jakarta.persistence.Table;

/** A stored compliance check: its report as JSON, and who ran it against which reference (V27). */
@Entity
@Table(name = "compliance_checks")
public class ComplianceCheckEntity {

    @Id
    @Column(length = 36)
    private String id;

    @Column(name = "checked_at", nullable = false)
    private Instant checkedAt;

    @Column(length = 63)
    private String reference;

    @Column(name = "reference_sha256", nullable = false, length = 64)
    private String referenceSha256;

    @Column(name = "cluster_id", length = 64)
    private String clusterId;

    @Column(nullable = false, length = 16)
    private String verdict;

    @Column(length = 64)
    private String actor;

    @Column(name = "principal_type", length = 16)
    private String principalType;

    @Column(name = "report_json", nullable = false, columnDefinition = "TEXT")
    private String reportJson;

    public ComplianceCheckEntity() {}

    public ComplianceCheckEntity(
            String id,
            Instant checkedAt,
            String reference,
            String referenceSha256,
            String clusterId,
            String verdict,
            String actor,
            String principalType,
            String reportJson) {
        this.id = id;
        this.checkedAt = checkedAt;
        this.reference = reference;
        this.referenceSha256 = referenceSha256;
        this.clusterId = clusterId;
        this.verdict = verdict;
        this.actor = actor;
        this.principalType = principalType;
        this.reportJson = reportJson;
    }

    public String getId() {
        return id;
    }

    public Instant getCheckedAt() {
        return checkedAt;
    }

    public String getReference() {
        return reference;
    }

    public String getReferenceSha256() {
        return referenceSha256;
    }

    public String getClusterId() {
        return clusterId;
    }

    public String getVerdict() {
        return verdict;
    }

    public String getActor() {
        return actor;
    }

    public String getPrincipalType() {
        return principalType;
    }

    public String getReportJson() {
        return reportJson;
    }
}
