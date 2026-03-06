ALTER TABLE test_runs ALTER COLUMN labels_json TYPE JSONB USING labels_json::jsonb;

CREATE INDEX IF NOT EXISTS idx_test_runs_labels ON test_runs USING GIN (labels_json);
