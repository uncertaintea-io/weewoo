package migrations

import (
	"context"
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
		Database:    "sqlite",
		DatabaseURL: filepath.Join(t.TempDir(), "weewoo.db"),
	}
	db, err := settings.OpenDatabase()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	require.NoError(t, Apply(context.Background(), db))
	require.NoError(t, Apply(context.Background(), db), "migrations must be idempotent")
	statuses, err := Statuses(context.Background(), db)
	require.NoError(t, err)
	require.Len(t, statuses, 13)
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

	manager := alerting.NewManager(db, cfg)
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
