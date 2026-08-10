DROP TABLE IF EXISTS service;

CREATE TABLE service (
    id INTEGER PRIMARY KEY,
    name TEXT,
    prometheus_url TEXT,
    load_query TEXT,
    latency_query TEXT,
    interval_seconds INTEGER,
    paused BOOLEAN NOT NULL DEFAULT false,
    revision INTEGER NOT NULL DEFAULT 1,
    generation INTEGER NOT NULL DEFAULT 1,
    baseline_reset_at TIMESTAMP
);
