CREATE INDEX IF NOT EXISTS idx_test_runs_type_date ON test_runs(test_type, created_at DESC);
