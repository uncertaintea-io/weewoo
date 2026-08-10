DROP TABLE IF EXISTS verdict;

CREATE TABLE verdict (
    service_id INTEGER,
    indicator_id INTEGER,
    "timestamp" TIMESTAMP,
    automated_good BOOLEAN,
    pvalue REAL,
    analysis_state TEXT NOT NULL DEFAULT 'pending'
        CHECK (analysis_state IN ('pending', 'baseline', 'good', 'bad', 'failed')),
    review_override BOOLEAN,
    review_revision INTEGER NOT NULL DEFAULT 0,
    reviewed_at TIMESTAMP,
    review_reason TEXT,
    generation INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (service_id, indicator_id, "timestamp"),
    FOREIGN KEY (service_id, indicator_id, "timestamp")
        REFERENCES time_chunk(service_id, indicator_id, "timestamp") ON DELETE CASCADE
);
