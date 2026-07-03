CREATE TABLE time_chunk (
    service_id int NOT NULL,
    indicator_id int NOT NULL,
    "timestamp" TIMESTAMP(0) WITH TIME ZONE NOT NULL,
    chunk bytea,

    PRIMARY KEY (service_id, indicator_id, "timestamp"),
    FOREIGN KEY (service_id) REFERENCES service(id)
);
