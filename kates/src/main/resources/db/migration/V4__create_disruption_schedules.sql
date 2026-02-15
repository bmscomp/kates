CREATE TABLE disruption_schedules (
    id              VARCHAR(36)  PRIMARY KEY,
    name            VARCHAR(128) NOT NULL,
    cron_expression VARCHAR(64)  NOT NULL,
    enabled         BOOLEAN      NOT NULL DEFAULT TRUE,
    playbook_name   VARCHAR(64),
    plan_json       TEXT,
    last_run_id     VARCHAR(36),
    last_run_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_disruption_sched_enabled ON disruption_schedules (enabled);
