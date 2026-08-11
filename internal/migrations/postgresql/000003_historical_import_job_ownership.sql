ALTER TABLE historical_import_job
    ADD COLUMN owner_id text,
    ADD COLUMN heartbeat_at TIMESTAMP WITH TIME ZONE;

CREATE INDEX historical_import_job_active_heartbeat_idx
    ON historical_import_job (heartbeat_at)
    WHERE state IN ('queued', 'running', 'building');
