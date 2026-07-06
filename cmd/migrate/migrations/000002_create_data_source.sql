DROP TABLE IF EXISTS data_source;

CREATE TABLE data_source (
    id int GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    type varchar,
    url varchar,
    polling_interval int
);
