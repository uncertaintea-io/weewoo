DROP TABLE IF EXISTS service;

CREATE TABLE service (
    id int GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name varchar,
    prometheus_url varchar,
    load_query varchar,
    latency_query varchar,
    interval_seconds int
);
