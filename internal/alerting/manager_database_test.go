package alerting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/config"
)

func TestTimeOfDayAnalysisMetadataReachesAlertAndNotification(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is not set")
	}
	db, err := sql.Open("pgx", databaseURL)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	ctx := context.Background()
	serviceID := int(time.Now().UnixNano()%1_000_000_000) + 1_000_000_000
	const indicatorID = loadTimeOfDayIndicatorID
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM alert WHERE service_id = $1`, serviceID)
		_, _ = db.ExecContext(ctx, `DELETE FROM time_chunk WHERE service_id = $1`, serviceID)
	})

	timestamp := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	_, err = db.ExecContext(ctx, `
		INSERT INTO time_chunk (service_id, indicator_id, "timestamp", chunk)
		VALUES ($1, $2, $3, '\x00')
	`, serviceID, indicatorID, timestamp)
	require.NoError(t, err)

	outcome := AnalysisOutcome{
		ServiceID:        serviceID,
		ServiceName:      "checkout",
		IndicatorID:      indicatorID,
		Timestamp:        timestamp,
		IndependentValue: 42,
		PValue:           0.001,
		Threshold:        0.01, Anomalous: true,
	}
	require.NoError(t, NewManager(db, config.NewFakeConfig()).RecordAnalysis(ctx, outcome))

	var alertID int64
	var title, description, impact, action string
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT id, title, description, impact, suggested_action
		FROM alert WHERE condition_key=$1
	`, anomalyConditionKey(serviceID, indicatorID, false)).Scan(
		&alertID, &title, &description, &impact, &action,
	))
	require.Contains(t, title, "Load vs. Time-of-Day")
	require.Contains(t, description, "Load")
	require.Contains(t, description, "time-of-day")

	var occurrenceSummary string
	var evidenceBody []byte
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT summary, evidence FROM alert_occurrence WHERE alert_id=$1
	`, alertID).Scan(&occurrenceSummary, &evidenceBody))
	require.Contains(t, occurrenceSummary, title)
	var evidence map[string]any
	require.NoError(t, json.Unmarshal(evidenceBody, &evidence))

	var payloadBody []byte
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT payload FROM alert_outbox WHERE alert_id=$1 AND operation='firing'
	`, alertID).Scan(&payloadBody))
	var payload outboxPayload
	require.NoError(t, json.Unmarshal(payloadBody, &payload))
	require.Equal(t, title, payload.Summary)
	require.Equal(t, description, payload.Description)
	require.Equal(t, impact, payload.Impact)
	require.Equal(t, action, payload.SuggestedAction)
	require.Equal(t, "2", payload.Indicator)
}

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

			manager := NewManager(db, config.NewFakeConfig(), "postgresql")
			outcome := AnalysisOutcome{
				ServiceID:   serviceID,
				ServiceName: "repro-service",
				IndicatorID: indicatorID,
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

func TestHistoricalAnalysisPersistsVerdictWithoutChangingAlerts(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is not set")
	}
	db, err := sql.Open("pgx", databaseURL)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	ctx := context.Background()
	serviceID := int(time.Now().UnixNano()%1_000_000_000) + 1_000_000_000
	const indicatorID = 72002
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM alert WHERE service_id = $1`, serviceID)
		_, _ = db.ExecContext(ctx, `DELETE FROM time_chunk WHERE service_id = $1`, serviceID)
	})

	failedAt := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	historicalAt := failedAt.Add(-24 * time.Hour)
	historicalFailureAt := historicalAt.Add(-time.Minute)
	for _, timestamp := range []time.Time{failedAt, historicalAt, historicalFailureAt} {
		_, err := db.ExecContext(ctx, `
			INSERT INTO time_chunk (service_id, indicator_id, "timestamp", chunk)
			VALUES ($1, $2, $3, '\x00')
		`, serviceID, indicatorID, timestamp)
		require.NoError(t, err)
	}

	manager := NewManager(db, config.NewFakeConfig(), "postgresql")
	require.NoError(t, manager.RecordAnalysisFailure(ctx, AnalysisOutcome{
		ServiceID: serviceID, ServiceName: "repro-service",
		IndicatorID: indicatorID, Timestamp: failedAt,
	}, errors.New("live analysis failed")))
	require.NoError(t, manager.RecordAnalysis(ctx, AnalysisOutcome{
		ServiceID: serviceID, ServiceName: "repro-service",
		IndicatorID: indicatorID, Timestamp: historicalAt,
		PValue: 0.001, Anomalous: true, Historical: true,
	}))
	require.NoError(t, manager.RecordAnalysisFailure(ctx, AnalysisOutcome{
		ServiceID: serviceID, ServiceName: "repro-service",
		IndicatorID: indicatorID, Timestamp: historicalFailureAt,
		Historical: true,
	}, errors.New("historical analysis failed")))

	var state string
	var good bool
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT analysis_state, automated_good
		FROM verdict
		WHERE service_id=$1 AND indicator_id=$2 AND "timestamp"=$3
	`, serviceID, indicatorID, historicalAt).Scan(&state, &good))
	require.Equal(t, "bad", state)
	require.False(t, good)

	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT analysis_state
		FROM verdict
		WHERE service_id=$1 AND indicator_id=$2 AND "timestamp"=$3
	`, serviceID, indicatorID, historicalFailureAt).Scan(&state))
	require.Equal(t, "failed", state)

	var alertCount int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT count(*) FROM alert WHERE service_id=$1
	`, serviceID).Scan(&alertCount))
	require.Equal(t, 1, alertCount, "historical analysis must not open an anomaly alert")

	var monitoringStatus string
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT status FROM alert WHERE condition_key=$1
	`, monitoringConditionKey(serviceID, "anomaly_analysis")).Scan(&monitoringStatus))
	require.Equal(t, StatusFiring, monitoringStatus, "historical success must not resolve a live monitoring failure")
}

func TestOutboxRetiresLegacyHistoricalFiringWithoutDelivery(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is not set")
	}
	db, err := sql.Open("pgx", databaseURL)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	ctx := context.Background()
	serviceID := int(time.Now().UnixNano()%1_000_000_000) + 1_000_000_000
	const indicatorID = 72003
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM alert WHERE service_id = $1`, serviceID)
		_, _ = db.ExecContext(ctx, `DELETE FROM time_chunk WHERE service_id = $1`, serviceID)
	})
	timestamp := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	_, err = db.ExecContext(ctx, `
		INSERT INTO time_chunk (service_id, indicator_id, "timestamp", chunk)
		VALUES ($1, $2, $3, '\x00')
	`, serviceID, indicatorID, timestamp)
	require.NoError(t, err)

	manager := NewManager(db, config.NewFakeConfig(), "postgresql")
	require.NoError(t, manager.recordAnomaly(ctx, AnalysisOutcome{
		ServiceID: serviceID, ServiceName: "legacy-service",
		IndicatorID: indicatorID, Timestamp: timestamp,
		PValue: 0.001, Anomalous: true, Historical: true,
	}))
	deliveries := 0
	dispatcher := &OutboxDispatcher{
		db: db, cfg: config.NewFakeConfig(), manager: manager, ctx: ctx,
		send: func(context.Context, config.Config, AlertingOptions) error {
			deliveries++
			return nil
		},
	}

	require.NoError(t, dispatcher.deliverOne())
	require.Zero(t, deliveries)

	var status, reason, outboxState string
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT status, resolution_reason FROM alert
		WHERE condition_key=$1
	`, anomalyConditionKey(serviceID, indicatorID, true)).Scan(&status, &reason))
	require.Equal(t, StatusResolved, status)
	require.Equal(t, "historical_notifications_disabled", reason)
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT state FROM alert_outbox
		WHERE alert_id=(SELECT id FROM alert WHERE condition_key=$1)
		  AND operation='firing'
	`, anomalyConditionKey(serviceID, indicatorID, true)).Scan(&outboxState))
	require.Equal(t, "missed", outboxState)
}
