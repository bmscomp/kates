-- Cooperative leases so cron-style schedulers fire on exactly one replica.
-- (The outbox drain is already replica-safe via FOR UPDATE SKIP LOCKED.)
CREATE TABLE IF NOT EXISTS scheduler_leases (
    name VARCHAR(64) PRIMARY KEY,
    holder VARCHAR(128) NOT NULL,
    expires_at TIMESTAMP NOT NULL
);
