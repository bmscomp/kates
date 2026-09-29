CREATE TABLE IF NOT EXISTS webhook_dlq (
    id SERIAL PRIMARY KEY,
    webhook_name VARCHAR(255) NOT NULL,
    url VARCHAR(2048) NOT NULL,
    payload TEXT NOT NULL,
    error_message TEXT,
    failed_at TIMESTAMP WITH TIME ZONE NOT NULL
);
