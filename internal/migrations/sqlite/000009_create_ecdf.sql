CREATE TABLE IF NOT EXISTS ecdf (
    service_id INTEGER NOT NULL,
    indicator_id INTEGER NOT NULL,
    version INTEGER NOT NULL,
    body BLOB NOT NULL,
    bytes INTEGER NOT NULL CHECK (bytes > 0 AND length(body) = bytes),
    sha256 TEXT NOT NULL CHECK (length(sha256) = 64 AND sha256 NOT GLOB '*[^0-9a-f]*'),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    interval_end TIMESTAMP,
    PRIMARY KEY (service_id, indicator_id, version)
);

CREATE INDEX IF NOT EXISTS ecdf_current_version_idx
    ON ecdf (service_id, indicator_id, version DESC);

CREATE UNIQUE INDEX IF NOT EXISTS ecdf_build_interval_idx
    ON ecdf (service_id, indicator_id, interval_end)
    WHERE interval_end IS NOT NULL;
