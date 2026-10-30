-- Each compliance check's report, so a check is evidence an auditor can be
-- shown later rather than a line in a pipeline's log (plans/enterprise.md,
-- Phase 3a). reference_sha256 is the SHA-256 of the body the Kates API
-- received. Nothing deletes these rows.
CREATE TABLE compliance_checks (
    id               VARCHAR(36)  PRIMARY KEY,
    checked_at       TIMESTAMPTZ  NOT NULL,
    reference        VARCHAR(63),
    reference_sha256 VARCHAR(64)  NOT NULL,
    cluster_id       VARCHAR(64),
    verdict          VARCHAR(16)  NOT NULL,
    actor            VARCHAR(64),
    principal_type   VARCHAR(16),
    report_json      TEXT         NOT NULL
);

CREATE INDEX idx_compliance_checks_checked ON compliance_checks (checked_at DESC);
CREATE INDEX idx_compliance_checks_cluster ON compliance_checks (cluster_id);
