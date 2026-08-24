-- flyway executeInTransaction=false
--
-- CREATE INDEX (the plain form) takes an ACCESS EXCLUSIVE lock for the whole
-- build, blocking every read and write on the table until it finishes. One of
-- the two tables here is processed_events, which this same wave describes as
-- growing forever — on a deployment that has been up for months, building its
-- index inline would stall the API for the duration of the upgrade. Built
-- CONCURRENTLY the index takes only a SHARE UPDATE EXCLUSIVE lock, so normal
-- traffic continues while it is created.
--
-- CONCURRENTLY cannot run inside a transaction block, hence the
-- executeInTransaction=false above. The trade-off is that a failure leaves an
-- INVALID index behind and marks the migration failed; recovery is
--   DROP INDEX CONCURRENTLY <name>;   then re-run the migration.
-- IF NOT EXISTS makes the re-run idempotent.

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_outbox_events_created_at ON outbox_events (created_at);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_processed_events_processed_at ON processed_events (processed_at);
