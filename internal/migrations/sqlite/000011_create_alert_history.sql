CREATE TABLE alert (
    id INTEGER PRIMARY KEY,
    condition_key TEXT NOT NULL,
    service_id INTEGER,
    service_name TEXT NOT NULL,
    indicator_id INTEGER,
    kind TEXT NOT NULL,
    severity TEXT NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    status TEXT NOT NULL CHECK (status IN ('firing', 'resolved')),
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    impact TEXT NOT NULL,
    suggested_action TEXT NOT NULL,
    technical_details TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMP NOT NULL,
    last_occurred_at TIMESTAMP NOT NULL,
    resolved_at TIMESTAMP,
    resolution_reason TEXT,
    retention_anchor TIMESTAMP,
    occurrence_count INTEGER NOT NULL DEFAULT 0 CHECK (occurrence_count >= 0),
    consecutive_count INTEGER NOT NULL DEFAULT 0 CHECK (consecutive_count >= 0),
    revision INTEGER NOT NULL DEFAULT 1,
    alertmanager_state TEXT NOT NULL DEFAULT 'pending'
        CHECK (alertmanager_state IN ('pending', 'accepted', 'failed', 'missed')),
    alertmanager_error TEXT,
    last_handoff_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX alert_one_firing_condition_idx ON alert (condition_key) WHERE status = 'firing';
CREATE INDEX alert_list_idx ON alert (status, last_occurred_at DESC);
CREATE INDEX alert_service_idx ON alert (service_id, last_occurred_at DESC);
CREATE INDEX alert_retention_idx ON alert (retention_anchor) WHERE status = 'resolved';

CREATE TABLE alert_occurrence (
    id INTEGER PRIMARY KEY,
    alert_id INTEGER NOT NULL REFERENCES alert(id) ON DELETE CASCADE,
    occurrence_key TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL,
    occurred_at TIMESTAMP NOT NULL,
    detected_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    window_start TIMESTAMP,
    window_end TIMESTAMP,
    service_id INTEGER,
    indicator_id INTEGER,
    chunk_timestamp TIMESTAMP,
    summary TEXT NOT NULL,
    technical_details TEXT NOT NULL DEFAULT '',
    evidence TEXT NOT NULL DEFAULT '{}',
    review_revision INTEGER NOT NULL DEFAULT 0,
    review_override BOOLEAN,
    reviewed_at TIMESTAMP,
    review_reason TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX alert_occurrence_alert_idx ON alert_occurrence (alert_id, occurred_at DESC);
CREATE UNIQUE INDEX alert_occurrence_chunk_idx
    ON alert_occurrence (service_id, indicator_id, chunk_timestamp) WHERE chunk_timestamp IS NOT NULL;

CREATE TABLE alert_event (
    id INTEGER PRIMARY KEY,
    alert_id INTEGER NOT NULL REFERENCES alert(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    message TEXT NOT NULL,
    metadata TEXT NOT NULL DEFAULT '{}',
    occurred_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX alert_event_alert_idx ON alert_event (alert_id, occurred_at DESC);

CREATE TABLE alert_outbox (
    id INTEGER PRIMARY KEY,
    alert_id INTEGER NOT NULL REFERENCES alert(id) ON DELETE CASCADE,
    operation TEXT NOT NULL CHECK (operation IN ('firing', 'resolved')),
    payload TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'delivered', 'missed')),
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    delivered_at TIMESTAMP,
    last_error TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX alert_outbox_pending_idx ON alert_outbox (next_attempt_at, id) WHERE state = 'pending';

CREATE TABLE collection_backlog (
    id INTEGER PRIMARY KEY,
    service_id INTEGER NOT NULL,
    service_name TEXT NOT NULL,
    window_start TIMESTAMP NOT NULL,
    window_end TIMESTAMP NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'collecting', 'recovered', 'expired')),
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NOT NULL,
    last_error TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
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
    ('ecdf_baseline_chunks', '10')
ON CONFLICT (key) DO NOTHING;
