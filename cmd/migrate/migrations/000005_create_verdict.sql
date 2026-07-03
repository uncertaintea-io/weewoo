CREATE TABLE verdict (
    service_id int,
    indicator_id int,
    "timestamp" TIMESTAMP(0) WITH TIME ZONE,
    good boolean,
    pvalue float,

    PRIMARY KEY (service_id, indicator_id, "timestamp"),
    FOREIGN KEY (service_id, indicator_id, "timestamp") REFERENCES time_chunk(service_id, indicator_id, "timestamp") ON DELETE CASCADE
);
