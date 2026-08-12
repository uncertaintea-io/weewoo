package migrations

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/alerting"
	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

func TestSQLiteMigrationsAndStores(t *testing.T) {
	settings := config.SystemSettings{
		Database:         "sqlite",
		ConnectionString: filepath.Join(t.TempDir(), "weewoo.db"),
	}
	db, err := settings.OpenDatabase()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	require.NoError(t, Apply(context.Background(), db, settings.Database))
	require.NoError(t, Apply(context.Background(), db, settings.Database), "migrations must be idempotent")
	statuses, err := Statuses(context.Background(), db, settings.Database)
	require.NoError(t, err)
	require.Len(t, statuses, 14)
	for _, status := range statuses {
		require.True(t, status.Applied, "migration %d was not applied", status.Version)
	}

	cfg := config.NewDatabaseConfig(db)
	service := &config.Service{
		Name: "sqlite-test", PrometheusURL: "http://example.com",
		LoadQuery: "load", LatencyQuery: "latency", Interval: time.Minute,
	}
	require.NoError(t, cfg.WriteService(service))
	require.NotZero(t, service.Id)
	stored, err := cfg.ReadService(service.Id)
	require.NoError(t, err)
	require.Equal(t, service.Name, stored.Name)

	chunkStore := ecdf.NewDatabaseChunkStore(db)
	timestamp := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, chunkStore.WriteChunk(service.Id, 1, service.Generation, timestamp, []byte("chunk")))
	chunk, err := chunkStore.ReadChunk(service.Id, 1, timestamp)
	require.NoError(t, err)
	require.Equal(t, []byte("chunk"), chunk)
	require.NoError(t, chunkStore.WriteVerdict(context.Background(), service.Id, 1, service.Generation, timestamp, true, 0.5))
	eligible, err := chunkStore.CountEligibleChunks(context.Background(), service.Id, 1, service.Generation)
	require.NoError(t, err)
	require.Equal(t, 1, eligible)

	manager := alerting.NewManager(db, cfg, settings.Database)
	outcome := alerting.AnalysisOutcome{
		ServiceID: service.Id, ServiceName: service.Name, IndicatorID: 1,
		Indicator: "load", Timestamp: timestamp, PValue: 0.001, Threshold: 0.01,
		Anomalous: true, Description: "SQLite anomaly test",
	}
	require.NoError(t, manager.RecordAnalysis(context.Background(), outcome))
	alerts, err := manager.List(context.Background(), false, 10)
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	require.Equal(t, alerting.StatusFiring, alerts[0].Status)
	outcome.Anomalous = false
	require.NoError(t, manager.RecordAnalysis(context.Background(), outcome))
	alerts, err = manager.List(context.Background(), true, 10)
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	require.Equal(t, alerting.StatusResolved, alerts[0].Status)
}

func TestParseFilenameRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"not-valid.sql", "000000_bad.sql", "000001_.sql"} {
		if _, _, err := parseFilename(name); err == nil {
			t.Errorf("parseFilename(%q) unexpectedly succeeded", name)
		}
	}
}

func TestSystemSettingsOpenSQLiteDatabase(t *testing.T) {
	settings := config.SystemSettings{
		Database:         "SQLite",
		ConnectionString: filepath.Join(t.TempDir(), "weewoo.db"),
	}
	db, err := settings.OpenDatabase()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Ping())
	assert.Equal(t, 1, db.Stats().MaxOpenConnections)

	var foreignKeys int
	require.NoError(t, db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys))
	assert.Equal(t, 1, foreignKeys)
}

func TestSQLiteTimestampsRoundTripAsTheSameInstant(t *testing.T) {
	settings := config.SystemSettings{
		Database:         "sqlite",
		ConnectionString: filepath.Join(t.TempDir(), "timestamps.db"),
	}
	db, err := settings.OpenDatabase()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	testTimestampRoundTrips(t, db)
}

func TestPostgreSQLTimestampsRoundTripAsTheSameInstant(t *testing.T) {
	databaseURL := os.Getenv("WEEWOO_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("WEEWOO_TEST_POSTGRES_URL is not set")
	}
	settings := config.SystemSettings{Database: "postgresql", ConnectionString: databaseURL}
	db, err := settings.OpenDatabase()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	var timezone string
	require.NoError(t, db.QueryRow(`SHOW timezone`).Scan(&timezone))
	assert.Equal(t, "UTC", timezone)
	testTimestampRoundTrips(t, db)
}

func testTimestampRoundTrips(t *testing.T, db *sql.DB) {
	t.Helper()
	require.NoError(t, db.Ping())
	_, err := db.Exec(`CREATE TEMPORARY TABLE timestamp_round_trip (value TIMESTAMP NOT NULL)`)
	require.NoError(t, err)

	springForward := time.Date(2026, time.March, 8, 2, 1, 0, 0, time.FixedZone("EST", -5*60*60))
	for name, input := range map[string]time.Time{
		"local now":      time.Now(),
		"UTC now":        time.Now().UTC(),
		"spring forward": springForward,
	} {
		t.Run(name, func(t *testing.T) {
			var output time.Time
			require.NoError(t, db.QueryRow(
				`INSERT INTO timestamp_round_trip (value) VALUES ($1) RETURNING value`,
				input,
			).Scan(&output))
			difference := input.Sub(output).Abs()
			assert.Less(t, difference, time.Microsecond,
				"timestamp changed instant: input=%s output=%s", input, output)
		})
	}
}

func TestSystemSettingsRejectsUnknownDatabase(t *testing.T) {
	_, err := (&config.SystemSettings{Database: "mysql", ConnectionString: "ignored"}).OpenDatabase()
	assert.EqualError(t, err, "database must be either postgresql or sqlite")
}
