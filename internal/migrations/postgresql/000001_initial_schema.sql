CREATE TABLE config (
    key varchar PRIMARY KEY,
    value varchar
);

CREATE TABLE data_source (
    id int GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    type varchar,
    url varchar,
    polling_interval int
);

CREATE TABLE service (
    id int GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name varchar,
    prometheus_url varchar,
    load_query varchar,
    latency_query varchar,
    interval_seconds int,
    paused boolean NOT NULL DEFAULT false,
    revision bigint NOT NULL DEFAULT 1,
    generation bigint NOT NULL DEFAULT 1,
    baseline_reset_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE service_revision (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
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
CREATE INDEX service_revision_history_idx ON service_revision (service_id, new_revision DESC);

CREATE TABLE time_chunk (
    service_id int NOT NULL,
    indicator_id int NOT NULL,
    "timestamp" TIMESTAMP(0) WITH TIME ZONE NOT NULL,
    chunk bytea,
    collected_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    generation bigint NOT NULL DEFAULT 1,
    PRIMARY KEY (service_id, indicator_id, "timestamp")
);

CREATE TABLE verdict (
    service_id int,
    indicator_id int,
    "timestamp" TIMESTAMP(0) WITH TIME ZONE,
    automated_good boolean,
    pvalue float,
    analysis_state varchar NOT NULL DEFAULT 'pending'
        CHECK (analysis_state IN ('pending', 'baseline', 'good', 'bad', 'failed')),
    review_override boolean,
    review_revision bigint NOT NULL DEFAULT 0,
    reviewed_at TIMESTAMP WITH TIME ZONE,
    review_reason text,
    generation bigint NOT NULL DEFAULT 1,
    PRIMARY KEY (service_id, indicator_id, "timestamp"),
    FOREIGN KEY (service_id, indicator_id, "timestamp")
        REFERENCES time_chunk(service_id, indicator_id, "timestamp") ON DELETE CASCADE
);

CREATE TABLE alert_sink (
    id int GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    type varchar,
    url varchar
);

CREATE TABLE ecdf (
    service_id int NOT NULL,
    indicator_id int NOT NULL,
    version bigint NOT NULL,
    body bytea NOT NULL,
    bytes bigint NOT NULL CHECK (bytes > 0 AND octet_length(body) = bytes),
    sha256 char(64) NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    interval_end TIMESTAMP WITH TIME ZONE,
    PRIMARY KEY (service_id, indicator_id, version)
);
CREATE INDEX ecdf_current_version_idx ON ecdf (service_id, indicator_id, version DESC);
CREATE UNIQUE INDEX ecdf_build_interval_idx
    ON ecdf (service_id, indicator_id, interval_end) WHERE interval_end IS NOT NULL;

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
CREATE UNIQUE INDEX alert_one_firing_condition_idx ON alert (condition_key) WHERE status = 'firing';
CREATE INDEX alert_list_idx ON alert (status, last_occurred_at DESC);
CREATE INDEX alert_service_idx ON alert (service_id, last_occurred_at DESC);
CREATE INDEX alert_retention_idx ON alert (retention_anchor) WHERE status = 'resolved';

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
CREATE INDEX alert_occurrence_alert_idx ON alert_occurrence (alert_id, occurred_at DESC);
CREATE UNIQUE INDEX alert_occurrence_chunk_idx
    ON alert_occurrence (service_id, indicator_id, chunk_timestamp) WHERE chunk_timestamp IS NOT NULL;

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
    state varchar NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'delivered', 'missed')),
    attempts int NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    delivered_at TIMESTAMP WITH TIME ZONE,
    last_error text,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX alert_outbox_pending_idx ON alert_outbox (next_attempt_at, id) WHERE state = 'pending';

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
    ON collection_backlog (service_id, window_start) WHERE state IN ('pending', 'collecting');

INSERT INTO config (key, value) VALUES
    ('alert_retention', '2160h'),
    ('alert_critical_consecutive', '3'),
    ('collection_critical_consecutive', '3'),
    ('monitoring_critical_consecutive', '3'),
    ('collection_probe_after', '1h'),
    ('collection_probe_interval', '1h'),
    ('collection_backlog_retention', '24h'),
    ('ecdf_baseline_chunks', '10');
