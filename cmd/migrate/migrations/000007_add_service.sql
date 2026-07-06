INSERT INTO service (
    id, 
    "name", 
    prometheus_url, 
    load_query, 
    latency_query, 
    interval_seconds
) VALUES (
    1, 
    'WeeWoo', 
    'http://pc0:9090', 
    'rate(histogram_count(sum by (app) (weewoo_http_request_duration_seconds{app="weewoo"}))[45s:]) * 15', 
    'histogram_quantile(0.99,weewoo_http_request_duration_seconds{app="weewoo"}) or on() vector(0)',
    60
);
