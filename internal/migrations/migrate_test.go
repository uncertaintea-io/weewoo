// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	require.Len(t, statuses, 1)
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
		Timestamp: timestamp, PValueTest: 0.001, PValueThreshold: 0.01, Anomalous: true,
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

func TestSQLiteSchemaTimestampsRoundTripAsTheSameInstant(t *testing.T) {
	settings := config.SystemSettings{
		Database:         "sqlite",
		ConnectionString: filepath.Join(t.TempDir(), "timestamps.db"),
	}
	db, err := settings.OpenDatabase()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	require.NoError(t, Apply(context.Background(), db, settings.Database))
	testSchemaTimestampRoundTrips(t, db)
}

func TestPostgreSQLSchemaTimestampsRoundTripAsTheSameInstant(t *testing.T) {
	databaseURL := os.Getenv("WEEWOO_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("WEEWOO_TEST_POSTGRES_URL is not set")
	}
	settings := config.SystemSettings{Database: "postgresql", ConnectionString: databaseURL}
	db, err := settings.OpenDatabase()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	db.SetMaxOpenConns(1)

	var timezone string
	require.NoError(t, db.QueryRow(`SHOW timezone`).Scan(&timezone))
	require.Equal(t, "UTC", timezone)

	schema := fmt.Sprintf("weewoo_migrations_test_%d", time.Now().UnixNano())
	_, err = db.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, dropErr := db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		require.NoError(t, dropErr)
	})
	_, err = db.Exec(`SET search_path TO ` + schema)
	require.NoError(t, err)
	require.NoError(t, Apply(context.Background(), db, settings.Database))
	testSchemaTimestampRoundTrips(t, db)
}

func testSchemaTimestampRoundTrips(t *testing.T, db *sql.DB) {
	t.Helper()
	require.NoError(t, db.Ping())

	springForward := time.Date(2026, time.March, 8, 2, 1, 0, 0, time.FixedZone("EST", -5*60*60))
	for name, input := range map[string]time.Time{
		"local now":      time.Now(),
		"UTC now":        time.Now().UTC(),
		"spring forward": springForward,
	} {
		t.Run(name, func(t *testing.T) {
			store := ecdf.NewDatabaseChunkStore(db)
			require.NoError(t, store.WriteChunk(1, 1, 1, input, []byte("chunk")))

			var output time.Time
			require.NoError(t, db.QueryRow(`SELECT "timestamp" FROM time_chunk`).Scan(&output))
			require.True(t, input.Truncate(time.Second).Equal(output),
				"timestamp changed instant: input=%s output=%s", input, output)
			chunk, err := store.ReadChunk(1, 1, input)
			require.NoError(t, err)
			require.Equal(t, []byte("chunk"), chunk)
			require.NoError(t, store.WriteVerdict(context.Background(), 1, 1, 1, input, true, 0.5))

			var state string
			require.NoError(t, db.QueryRow(`SELECT analysis_state FROM verdict`).Scan(&state))
			require.Equal(t, "good", state)
			_, err = db.Exec(`DELETE FROM time_chunk WHERE service_id = $1 AND indicator_id = $2`, 1, 1)
			require.NoError(t, err)
		})
	}
}
