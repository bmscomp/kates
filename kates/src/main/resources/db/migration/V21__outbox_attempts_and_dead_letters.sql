-- A row whose payload cannot be deserialised, or whose send fails permanently,
-- used to be retried forever: the poller reads the oldest 50 rows every 2s, so
-- a poison pill sat at the head of that window and consumed one of the 50 slots
-- on every poll, for the life of the deployment. With enough of them the outbox
-- stops draining entirely while the table keeps growing.
--
-- Track attempts so a row can be given up on, and keep what was given up on:
-- silently dropping an event would defeat the point of an outbox.
ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS attempts INT NOT NULL DEFAULT 0;
ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS last_error TEXT;

CREATE TABLE IF NOT EXISTS outbox_dead_letters (
    id UUID PRIMARY KEY,
    aggregate_id VARCHAR(255) NOT NULL,
    aggregate_type VARCHAR(255) NOT NULL,
    event_type VARCHAR(255) NOT NULL,
    payload TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    failed_at TIMESTAMP NOT NULL,
    attempts INT NOT NULL,
    last_error TEXT
);

-- The table is created empty, so a plain (non-CONCURRENT) index here is
-- instantaneous and takes no meaningful lock.
CREATE INDEX IF NOT EXISTS idx_outbox_dead_letters_failed_at ON outbox_dead_letters (failed_at);
