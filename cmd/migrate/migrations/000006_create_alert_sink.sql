CREATE TABLE alert_sink (
    id int GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    type varchar,
    url varchar
);
