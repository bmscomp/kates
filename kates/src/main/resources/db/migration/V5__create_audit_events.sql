CREATE TABLE audit_events (
    id          BIGSERIAL    PRIMARY KEY,
    action      VARCHAR(32)  NOT NULL,
    event_type  VARCHAR(32)  NOT NULL,
    target      VARCHAR(128),
    details     TEXT,
    created_at  TIMESTAMP    NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_events_type ON audit_events (event_type);
CREATE INDEX idx_audit_events_created ON audit_events (created_at);
