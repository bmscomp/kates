-- Webhook registrations were held in memory only (CopyOnWriteArrayList) and
-- silently vanished on every restart, while their DLQ was already persisted.
CREATE TABLE IF NOT EXISTS webhook_registrations (
    name VARCHAR(255) PRIMARY KEY,
    url VARCHAR(2048) NOT NULL,
    events VARCHAR(255),
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);
