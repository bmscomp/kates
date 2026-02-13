CREATE TABLE test_runs (
    id              VARCHAR(36) PRIMARY KEY,
    test_type       VARCHAR(32),
    status          VARCHAR(16),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    backend         VARCHAR(32),
    scenario_name   VARCHAR(128),
    spec_json       TEXT,
    sla_json        TEXT,
    labels_json     TEXT
);

CREATE INDEX idx_test_runs_type ON test_runs (test_type);
CREATE INDEX idx_test_runs_created ON test_runs (created_at DESC);
CREATE INDEX idx_test_runs_status ON test_runs (status);

CREATE TABLE test_results (
    id                      BIGSERIAL PRIMARY KEY,
    test_run_id             VARCHAR(36) NOT NULL REFERENCES test_runs(id) ON DELETE CASCADE,
    task_id                 VARCHAR(128),
    test_type               VARCHAR(32),
    status                  VARCHAR(16),
    records_sent            BIGINT DEFAULT 0,
    throughput_rec_per_sec  DOUBLE PRECISION DEFAULT 0,
    throughput_mb_per_sec   DOUBLE PRECISION DEFAULT 0,
    avg_latency_ms          DOUBLE PRECISION DEFAULT 0,
    p50_latency_ms          DOUBLE PRECISION DEFAULT 0,
    p95_latency_ms          DOUBLE PRECISION DEFAULT 0,
    p99_latency_ms          DOUBLE PRECISION DEFAULT 0,
    max_latency_ms          DOUBLE PRECISION DEFAULT 0,
    start_time              VARCHAR(64),
    end_time                VARCHAR(64),
    error                   TEXT,
    phase_name              VARCHAR(128)
);

CREATE INDEX idx_test_results_run ON test_results (test_run_id);
