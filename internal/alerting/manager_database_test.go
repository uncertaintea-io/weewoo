package alerting

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/config"
)

func TestSuccessfulAnalysisResolvesAnalysisMonitoringFailure(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is not set")
	}
	db, err := sql.Open("pgx", databaseURL)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	ctx := context.Background()
	baseServiceID := int(time.Now().UnixNano()%1_000_000_000) + 1_000_000_000
	const indicatorID = 72001

	for index, test := range []struct {
		name      string
		anomalous bool
	}{
		{name: "good"},
		{name: "anomalous", anomalous: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			serviceID := baseServiceID + index
			t.Cleanup(func() {
				_, _ = db.ExecContext(ctx, `DELETE FROM alert WHERE service_id = $1`, serviceID)
				_, _ = db.ExecContext(ctx, `DELETE FROM time_chunk WHERE service_id = $1`, serviceID)
			})

			failedAt := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
			succeededAt := failedAt.Add(time.Minute)
			for _, timestamp := range []time.Time{failedAt, succeededAt} {
				_, err := db.ExecContext(ctx, `
					INSERT INTO time_chunk (service_id, indicator_id, "timestamp", chunk)
					VALUES ($1, $2, $3, '\x00')
				`, serviceID, indicatorID, timestamp)
				require.NoError(t, err)
			}

			manager := NewManager(db, config.NewFakeConfig())
			outcome := AnalysisOutcome{
				ServiceID:   serviceID,
				ServiceName: "repro-service",
				IndicatorID: indicatorID,
				Indicator:   "latency",
				Timestamp:   failedAt,
			}
			require.NoError(t, manager.RecordAnalysisFailure(
				ctx,
				outcome,
				errors.New("transient analysis error"),
			))

			conditionKey := monitoringConditionKey(serviceID, "anomaly_analysis")
			result, err := db.ExecContext(ctx, `
				UPDATE alert_outbox SET state = 'delivered'
				WHERE alert_id = (
					SELECT id FROM alert WHERE condition_key = $1 AND status = 'firing'
				) AND operation = 'firing'
			`, conditionKey)
			require.NoError(t, err)
			delivered, err := result.RowsAffected()
			require.NoError(t, err)
			require.EqualValues(t, 1, delivered)

			outcome.Timestamp = succeededAt
			outcome.PValue = 0.8
			outcome.Anomalous = test.anomalous
			require.NoError(t, manager.RecordAnalysis(ctx, outcome))

			var alertID int64
			var status string
			require.NoError(t, db.QueryRowContext(ctx, `
				SELECT id, status FROM alert
				WHERE condition_key = $1
				ORDER BY id DESC LIMIT 1
			`, conditionKey).Scan(&alertID, &status))
			require.Equal(t, StatusResolved, status)

			var resolutions int
			require.NoError(t, db.QueryRowContext(ctx, `
				SELECT count(*) FROM alert_outbox
				WHERE alert_id = $1 AND operation = 'resolved'
			`, alertID).Scan(&resolutions))
			require.Equal(t, 1, resolutions, "alert %d", alertID)
		})
	}
}
