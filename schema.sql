CREATE TABLE data_source (Id int GENERATED ALWAYS AS IDENTITY PRIMARY KEY, DataType varchar, URL varchar, PollingInterval int);
CREATE TABLE config (key varchar, value varchar);
CREATE TABLE alert_sink (Id int PRIMARY KEY, type varchar, URL varchar);
CREATE TABLE time_chunk (ServiceId int, IndicatorId int, SampleTimestamp int, Chunk bytea);
CREATE TABLE service (Id int PRIMARY KEY, Name varchar);
CREATE TABLE verdict (SampleTimestamp int, Good boolean, Pvalue float);
