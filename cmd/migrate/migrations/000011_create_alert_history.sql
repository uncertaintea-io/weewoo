ALTER TABLE time_chunk
    ADD COLUMN collected_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP;

ALTER TABLE verdict RENAME COLUMN good TO automated_good;
ALTER TABLE verdict
    ADD COLUMN analysis_state varchar NOT NULL DEFAULT 'pending',
    ADD COLUMN review_override boolean,
    ADD COLUMN review_revision bigint NOT NULL DEFAULT 0,
    ADD COLUMN reviewed_at TIMESTAMP WITH TIME ZONE,
    ADD COLUMN review_reason text;

UPDATE verdict
SET analysis_state = CASE
    WHEN automated_good = true THEN 'good'
    WHEN automated_good = false THEN 'bad'
    ELSE 'pending'
END;

ALTER TABLE verdict
    ADD CONSTRAINT verdict_analysis_state_check
    CHECK (analysis_state IN ('pending', 'baseline', 'good', 'bad', 'failed'));

CREATE TABLE alert (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    condition_key text NOT NULL,
    service_id int,
    service_name text NOT NULL,
    indicator_id int,
    kind varchar NOT NULL,
    severity varchar NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    status varchar NOT NULL CHECK (status IN ('firing', 'resolved')),
    title text NOT NULL,
    description text NOT NULL,
    impact text NOT NULL,
    suggested_action text NOT NULL,
    technical_details text NOT NULL DEFAULT '',
    started_at TIMESTAMP WITH TIME ZONE NOT NULL,
    last_occurred_at TIMESTAMP WITH TIME ZONE NOT NULL,
    resolved_at TIMESTAMP WITH TIME ZONE,
    resolution_reason varchar,
    retention_anchor TIMESTAMP WITH TIME ZONE,
    occurrence_count int NOT NULL DEFAULT 0 CHECK (occurrence_count >= 0),
    consecutive_count int NOT NULL DEFAULT 0 CHECK (consecutive_count >= 0),
    revision bigint NOT NULL DEFAULT 1,
    alertmanager_state varchar NOT NULL DEFAULT 'pending'
        CHECK (alertmanager_state IN ('pending', 'accepted', 'failed', 'missed')),
    alertmanager_error text,
    last_handoff_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX alert_one_firing_condition_idx
    ON alert (condition_key)
    WHERE status = 'firing';
CREATE INDEX alert_list_idx ON alert (status, last_occurred_at DESC);
CREATE INDEX alert_service_idx ON alert (service_id, last_occurred_at DESC);
CREATE INDEX alert_retention_idx ON alert (retention_anchor)
    WHERE status = 'resolved';

CREATE TABLE alert_occurrence (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    alert_id bigint NOT NULL REFERENCES alert(id) ON DELETE CASCADE,
    occurrence_key text NOT NULL UNIQUE,
    kind varchar NOT NULL,
    occurred_at TIMESTAMP WITH TIME ZONE NOT NULL,
    detected_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    window_start TIMESTAMP WITH TIME ZONE,
    window_end TIMESTAMP WITH TIME ZONE,
    service_id int,
    indicator_id int,
    chunk_timestamp TIMESTAMP WITH TIME ZONE,
    summary text NOT NULL,
    technical_details text NOT NULL DEFAULT '',
    evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
    review_revision bigint NOT NULL DEFAULT 0,
    review_override boolean,
    reviewed_at TIMESTAMP WITH TIME ZONE,
    review_reason text,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX alert_occurrence_alert_idx
    ON alert_occurrence (alert_id, occurred_at DESC);
CREATE UNIQUE INDEX alert_occurrence_chunk_idx
    ON alert_occurrence (service_id, indicator_id, chunk_timestamp)
    WHERE chunk_timestamp IS NOT NULL;

CREATE TABLE alert_event (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    alert_id bigint NOT NULL REFERENCES alert(id) ON DELETE CASCADE,
    type varchar NOT NULL,
    message text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX alert_event_alert_idx ON alert_event (alert_id, occurred_at DESC);

CREATE TABLE alert_outbox (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    alert_id bigint NOT NULL REFERENCES alert(id) ON DELETE CASCADE,
    operation varchar NOT NULL CHECK (operation IN ('firing', 'resolved')),
    payload jsonb NOT NULL,
    state varchar NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'delivered', 'missed')),
    attempts int NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    delivered_at TIMESTAMP WITH TIME ZONE,
    last_error text,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX alert_outbox_pending_idx
    ON alert_outbox (next_attempt_at, id)
    WHERE state = 'pending';

CREATE TABLE collection_backlog (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    service_id int NOT NULL,
    service_name text NOT NULL,
    window_start TIMESTAMP WITH TIME ZONE NOT NULL,
    window_end TIMESTAMP WITH TIME ZONE NOT NULL,
    state varchar NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'collecting', 'recovered', 'expired')),
    attempts int NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    last_error text,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (service_id, window_start, window_end)
);

CREATE INDEX collection_backlog_pending_idx
    ON collection_backlog (service_id, window_start)
    WHERE state IN ('pending', 'collecting');

INSERT INTO config (key, value) VALUES
    ('alert_retention', '2160h'),
    ('alert_critical_consecutive', '3'),
    ('collection_critical_consecutive', '3'),
    ('monitoring_critical_consecutive', '3'),
    ('collection_probe_after', '1h'),
    ('collection_probe_interval', '1h'),
    ('collection_backlog_retention', '24h'),
    ('ecdf_baseline_chunks', '10')
ON CONFLICT (key) DO NOTHING;
