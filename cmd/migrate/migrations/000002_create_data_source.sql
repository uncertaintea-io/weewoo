CREATE TABLE IF NOT EXISTS data_source (
    id int GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    type varchar,
    url varchar,
    polling_interval int
);
