CREATE TABLE data_source ( id int PRIMARY KEY, data_type varchar, url varchar, polling_interval int);
CREATE TABLE config (key varchar, value varchar);
CREATE TABLE alert_sink (id int PRIMARY KEY, type varchar, url varchar);
CREATE TABLE time_chunk (service_id int, indicator_id int, sample_timestamp int, chunk bytea);
CREATE TABLE service (id int PRIMARY KEY, name varchar);
CREATE TABLE verdict (sample_timestamp int, good boolean, pvalue float);
