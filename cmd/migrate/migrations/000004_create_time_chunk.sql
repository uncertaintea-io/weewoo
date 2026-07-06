DROP TABLE IF EXISTS time_chunk;

CREATE TABLE time_chunk (
    service_id int NOT NULL,
    indicator_id int NOT NULL,
    "timestamp" TIMESTAMP(0) WITH TIME ZONE NOT NULL,
    chunk bytea,

    PRIMARY KEY (service_id, indicator_id, "timestamp")

    -- TODO: Add foreign key constraint once the hard-coded WeeWoo service has been added to the database.
    -- FOREIGN KEY (service_id) REFERENCES service(id)
);
