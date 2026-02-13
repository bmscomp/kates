CREATE TABLE scheduled_test_runs (
    id              VARCHAR(36) PRIMARY KEY,
    name            VARCHAR(128) NOT NULL,
    cron_expression VARCHAR(64)  NOT NULL,
    enabled         BOOLEAN      NOT NULL DEFAULT TRUE,
    request_json    TEXT         NOT NULL,
    last_run_id     VARCHAR(36),
    last_run_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_schedules_enabled ON scheduled_test_runs (enabled);
