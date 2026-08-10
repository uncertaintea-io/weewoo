DROP TABLE IF EXISTS time_chunk;

CREATE TABLE time_chunk (
    service_id INTEGER NOT NULL,
    indicator_id INTEGER NOT NULL,
    "timestamp" TIMESTAMP NOT NULL,
    chunk BLOB,
    collected_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    generation INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (service_id, indicator_id, "timestamp")
);
