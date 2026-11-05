-- The caller's Idempotency-Key on POST /api/tests. Null on runs created
-- without one, and PostgreSQL lets a unique index hold any number of nulls.
ALTER TABLE test_runs ADD COLUMN client_key VARCHAR(128);

CREATE UNIQUE INDEX idx_test_runs_client_key ON test_runs (client_key);
