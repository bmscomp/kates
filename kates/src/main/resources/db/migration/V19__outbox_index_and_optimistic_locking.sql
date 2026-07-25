-- P3-1: the outbox poller runs every 2s with
--   SELECT ... FROM outbox_events ORDER BY created_at ASC ... FOR UPDATE SKIP LOCKED
-- and the table had only its primary key, so every poll was a full scan + sort.
CREATE INDEX IF NOT EXISTS idx_outbox_events_created_at ON outbox_events (created_at);

-- processed_events is the consumer-side idempotency ledger; it grows forever and
-- is pruned by age, which needs an index on the age column.
CREATE INDEX IF NOT EXISTS idx_processed_events_processed_at ON processed_events (processed_at);

-- P3-3: optimistic locking for test_runs. refreshStatus, stopTest, the timeout
-- reaper and orphan recovery all read-modify-write the same row; without a
-- version column the last writer silently won, letting the reaper clobber a
-- completion that landed while it was working.
ALTER TABLE test_runs ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 0;
