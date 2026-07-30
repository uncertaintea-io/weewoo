ALTER TABLE service
    ADD COLUMN revision bigint NOT NULL DEFAULT 1,
    ADD COLUMN generation bigint NOT NULL DEFAULT 1,
    ADD COLUMN baseline_reset_at TIMESTAMP WITH TIME ZONE;

CREATE TABLE service_revision (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- Deliberately no foreign key: deleting a service must not erase its audit trail.
    service_id int NOT NULL,
    previous_revision bigint NOT NULL,
    new_revision bigint NOT NULL,
    changed_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    changed_by text NOT NULL,
    material boolean NOT NULL,
    previous_configuration jsonb NOT NULL,
    new_configuration jsonb NOT NULL,
    UNIQUE (service_id, new_revision)
);

CREATE INDEX service_revision_history_idx
    ON service_revision (service_id, new_revision DESC);
