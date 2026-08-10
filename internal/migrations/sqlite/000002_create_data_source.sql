DROP TABLE IF EXISTS data_source;

CREATE TABLE data_source (
    id INTEGER PRIMARY KEY,
    type TEXT,
    url TEXT,
    polling_interval INTEGER
);
