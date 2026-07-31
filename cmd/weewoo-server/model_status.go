package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/collection"
	"github.com/uncertaintea-io/weewoo/internal/config"
)

type databaseModelStatusReader struct{ db *sql.DB }

func (r *databaseModelStatusReader) TimeOfDayStatus(ctx context.Context, service *config.Service) (timeOfDayModelStatus, error) {
	status := timeOfDayModelStatus{State: "learning", RequiredDays: 5}
	if service.Interval <= 0 {
		return status, fmt.Errorf("invalid service interval")
	}
	total := int((24*time.Hour + service.Interval - 1) / service.Interval)
	var qualified int
	err := r.db.QueryRowContext(ctx, `
		SELECT count(*) FROM (
			SELECT floor(extract(epoch FROM (tc."timestamp" AT TIME ZONE 'UTC')::time) / $4) AS bucket
			FROM time_chunk tc JOIN verdict v USING (service_id, indicator_id, "timestamp")
			WHERE tc.service_id=$1 AND tc.indicator_id=$2 AND tc.generation=$3
			  AND (v.analysis_state IN ('baseline','good') OR (v.analysis_state='bad' AND v.review_override=true))
			GROUP BY bucket HAVING count(DISTINCT (tc."timestamp" AT TIME ZONE 'UTC')::date) >= 5
		) qualified
	`, service.Id, collection.TimeOfDayIndicator, service.Generation, service.Interval.Seconds()).Scan(&qualified)
	if err != nil {
		return status, fmt.Errorf("read time-of-day coverage: %w", err)
	}
	status.Coverage = float64(qualified) / float64(total)
	if status.Coverage >= .95 {
		status.State = "ready"
	}
	var latest sql.NullTime
	if err := r.db.QueryRowContext(ctx, `SELECT max(created_at) FROM ecdf WHERE service_id=$1 AND indicator_id=$2`, service.Id, collection.TimeOfDayIndicator).Scan(&latest); err != nil {
		return status, err
	}
	if latest.Valid {
		status.LatestBuild = &latest.Time
	}
	return status, nil
}
