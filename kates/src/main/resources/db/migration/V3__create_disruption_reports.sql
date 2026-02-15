CREATE TABLE disruption_reports (
    id           VARCHAR(36)  PRIMARY KEY,
    plan_name    VARCHAR(128) NOT NULL,
    status       VARCHAR(16)  NOT NULL,
    sla_grade    VARCHAR(2),
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    report_json  TEXT         NOT NULL,
    summary_json TEXT
);

CREATE INDEX idx_disruption_plan    ON disruption_reports (plan_name);
CREATE INDEX idx_disruption_created ON disruption_reports (created_at DESC);
CREATE INDEX idx_disruption_status  ON disruption_reports (status);
