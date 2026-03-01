CREATE TABLE IF NOT EXISTS snapshots (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(128) NOT NULL UNIQUE,
    context VARCHAR(128),
    brokers INT,
    topics INT,
    consumer_groups INT,
    topic_list JSONB,
    group_list JSONB,
    metadata JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);
