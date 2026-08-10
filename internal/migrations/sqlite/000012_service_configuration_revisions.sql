CREATE TABLE service_revision (
    id INTEGER PRIMARY KEY,
    service_id INTEGER NOT NULL,
    previous_revision INTEGER NOT NULL,
    new_revision INTEGER NOT NULL,
    changed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    changed_by TEXT NOT NULL,
    material BOOLEAN NOT NULL,
    previous_configuration TEXT NOT NULL,
    new_configuration TEXT NOT NULL,
    UNIQUE (service_id, new_revision)
);

CREATE INDEX service_revision_history_idx ON service_revision (service_id, new_revision DESC);
