DROP TABLE IF EXISTS alert_sink;

CREATE TABLE alert_sink (
    id INTEGER PRIMARY KEY,
    type TEXT,
    url TEXT
);
