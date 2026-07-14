ALTER TABLE ecdf
    ADD COLUMN interval_end TIMESTAMP WITH TIME ZONE;

WITH ranked_versions AS (
    SELECT
        service_id,
        indicator_id,
        version,
        date_trunc('hour', created_at) AS inferred_interval_end,
        row_number() OVER (
            PARTITION BY service_id, indicator_id, date_trunc('hour', created_at)
            ORDER BY version DESC
        ) AS interval_rank
    FROM ecdf
)
UPDATE ecdf
SET interval_end = ranked_versions.inferred_interval_end
FROM ranked_versions
WHERE ecdf.service_id = ranked_versions.service_id
  AND ecdf.indicator_id = ranked_versions.indicator_id
  AND ecdf.version = ranked_versions.version
  AND ranked_versions.interval_rank = 1;

CREATE UNIQUE INDEX ecdf_build_interval_idx
    ON ecdf (service_id, indicator_id, interval_end)
    WHERE interval_end IS NOT NULL;
