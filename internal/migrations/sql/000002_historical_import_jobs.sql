CREATE TABLE historical_import_job (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    service_id int NOT NULL REFERENCES service(id) ON DELETE CASCADE,
    state varchar NOT NULL CHECK (state IN ('queued', 'running', 'building', 'complete', 'complete_with_gaps', 'failed', 'cancelled')),
    progress int NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    total_windows int NOT NULL DEFAULT 0 CHECK (total_windows >= 0),
    imported_windows int NOT NULL DEFAULT 0 CHECK (imported_windows >= 0),
    gap_windows int NOT NULL DEFAULT 0 CHECK (gap_windows >= 0),
    error text,
    range_start TIMESTAMP WITH TIME ZONE NOT NULL,
    range_end TIMESTAMP WITH TIME ZONE NOT NULL,
    started_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ended_at TIMESTAMP WITH TIME ZONE,
    CHECK (range_start < range_end)
);

CREATE INDEX historical_import_job_service_idx
    ON historical_import_job (service_id, id DESC);
