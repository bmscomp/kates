ALTER TABLE test_runs ADD COLUMN IF NOT EXISTS tags JSONB DEFAULT '[]'::jsonb;

CREATE INDEX IF NOT EXISTS idx_test_runs_tags ON test_runs USING GIN (tags);
