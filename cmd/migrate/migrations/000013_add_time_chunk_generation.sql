ALTER TABLE time_chunk
    ADD COLUMN generation bigint NOT NULL DEFAULT 1;

ALTER TABLE verdict
    ADD COLUMN generation bigint NOT NULL DEFAULT 1;
