-- Chains the audit log (plans/enterprise.md, Phase 3b): each new row holds
-- the hash of the row before it and its own, so an edit or a deletion made
-- in the database shows up in GET /api/audit/verify. audit_chain_head holds
-- the newest row's hash; locking it orders the writers, replicas included.
-- Rows written before this have no hash, and the chain starts after them.
ALTER TABLE audit_events ADD COLUMN prev_hash VARCHAR(64);
ALTER TABLE audit_events ADD COLUMN row_hash VARCHAR(64);

CREATE TABLE audit_chain_head (
    id        INTEGER     PRIMARY KEY,
    last_id   BIGINT,
    last_hash VARCHAR(64) NOT NULL
);

INSERT INTO audit_chain_head (id, last_id, last_hash)
VALUES (1, NULL, '0000000000000000000000000000000000000000000000000000000000000000');
