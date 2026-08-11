CREATE TABLE historical_import_job (
    id INTEGER PRIMARY KEY,
    service_id INTEGER NOT NULL REFERENCES service(id) ON DELETE CASCADE,
    state TEXT NOT NULL CHECK (state IN ('queued', 'running', 'building', 'complete', 'complete_with_gaps', 'failed', 'cancelled')),
    progress INTEGER NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    total_windows INTEGER NOT NULL DEFAULT 0 CHECK (total_windows >= 0),
    imported_windows INTEGER NOT NULL DEFAULT 0 CHECK (imported_windows >= 0),
    gap_windows INTEGER NOT NULL DEFAULT 0 CHECK (gap_windows >= 0),
    error TEXT,
    range_start TIMESTAMP NOT NULL,
    range_end TIMESTAMP NOT NULL,
    started_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ended_at TIMESTAMP,
    owner_id TEXT,
    heartbeat_at TIMESTAMP,
    CHECK (range_start < range_end)
);

CREATE INDEX historical_import_job_service_idx
    ON historical_import_job (service_id, id DESC);

CREATE INDEX historical_import_job_active_heartbeat_idx
    ON historical_import_job (heartbeat_at)
    WHERE state IN ('queued', 'running', 'building');
