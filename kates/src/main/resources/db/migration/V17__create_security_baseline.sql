-- Security baseline snapshots were a volatile in-memory field; drift
-- comparisons broke on every restart. Single-row table keyed by 'current'.
CREATE TABLE IF NOT EXISTS security_baseline (
    id VARCHAR(32) PRIMARY KEY,
    payload TEXT NOT NULL,
    saved_at TIMESTAMP NOT NULL DEFAULT NOW()
);
