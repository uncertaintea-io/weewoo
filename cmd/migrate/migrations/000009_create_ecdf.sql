CREATE TABLE IF NOT EXISTS ecdf (
    service_id int NOT NULL,
    indicator_id int NOT NULL,
    version bigint NOT NULL,
    body bytea NOT NULL,
    bytes bigint NOT NULL CHECK (bytes > 0 AND octet_length(body) = bytes),
    sha256 char(64) NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY (service_id, indicator_id, version)
);

CREATE INDEX IF NOT EXISTS ecdf_current_version_idx
    ON ecdf (service_id, indicator_id, version DESC);
