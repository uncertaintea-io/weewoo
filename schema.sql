CREATE TABLE data_source (Id int GENERATED ALWAYS AS IDENTITY PRIMARY KEY, DataType varchar, URL varchar, PollingInterval int);
CREATE TABLE config (key varchar, value varchar);
CREATE TABLE alert_sink (Id int PRIMARY KEY, type varchar, URL varchar);
CREATE TABLE time_chunk (service_id int, indicator_id int, "Timestamp" TIMESTAMP(0) WITH TIME ZONE, chunk bytea);
CREATE TABLE service (Id int PRIMARY KEY, Name varchar);
CREATE TABLE verdict (SampleTimestamp int, Good boolean, Pvalue float);
