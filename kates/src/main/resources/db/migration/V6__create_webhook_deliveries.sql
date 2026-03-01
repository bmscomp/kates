CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id BIGSERIAL PRIMARY KEY,
    webhook_url VARCHAR(2048) NOT NULL,
    run_id VARCHAR(64) NOT NULL,
    event_type VARCHAR(32) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'PENDING',
    attempts INT NOT NULL DEFAULT 0,
    response_code INT,
    error_message TEXT,
    payload JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    delivered_at TIMESTAMP
);

CREATE INDEX idx_webhook_del_run ON webhook_deliveries(run_id);
CREATE INDEX idx_webhook_del_status ON webhook_deliveries(status);
