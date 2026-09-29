CREATE TABLE IF NOT EXISTS processed_events (
    idempotency_key VARCHAR(128) PRIMARY KEY,
    processed_at TIMESTAMP WITH TIME ZONE NOT NULL
);
