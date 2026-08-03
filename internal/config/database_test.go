package config

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBaselinePublicationLocksCoverEveryIndicator(t *testing.T) {
	assert.ElementsMatch(t, []int{1, 2}, baselinePublicationIndicatorIDs)
}

func TestMaterialServiceUpdateWaitsForInFlightBaselinePublication(t *testing.T) {
	for name, indicatorID := range map[string]int{
		"load-latency": loadLatencyBaselineIndicatorID,
		"time-of-day":  timeOfDayBaselineIndicatorID,
	} {
		t.Run(name, func(t *testing.T) {
			testMaterialServiceUpdateWaitsForInFlightBaselinePublication(t, indicatorID)
		})
	}
}

func testMaterialServiceUpdateWaitsForInFlightBaselinePublication(t *testing.T, indicatorID int) {
	connString := os.Getenv("DATABASE_URL")
	if connString == "" {
		t.Skip("DATABASE_URL is not set")
	}
	db, err := sql.Open("pgx", connString)
	require.NoError(t, err)
	defer db.Close()
	store := &database{db: db}
	service := &Service{
		Name: "baseline-lock-test", PrometheusURL: "http://example.com",
		LoadQuery: "old_load", LatencyQuery: "latency", Interval: time.Minute,
	}
	require.NoError(t, store.WriteService(service))
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM service_revision WHERE service_id=$1`, service.Id)
		_, _ = db.Exec(`DELETE FROM ecdf WHERE service_id=$1`, service.Id)
		_, _ = db.Exec(`DELETE FROM service WHERE id=$1`, service.Id)
	})

	publisher, err := db.Conn(context.Background())
	require.NoError(t, err)
	defer publisher.Close()
	var locked bool
	require.NoError(t, publisher.QueryRowContext(context.Background(),
		`SELECT pg_try_advisory_lock($1,$2)`, service.Id, indicatorID).Scan(&locked))
	require.True(t, locked)

	updated := *service
	updated.LoadQuery = "new_load"
	updateDone := make(chan error, 1)
	go func() {
		updateDone <- store.UpdateService(context.Background(), &updated, service.Revision, "test")
	}()
	select {
	case err := <-updateDone:
		t.Fatalf("material update did not wait for baseline publisher: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	body := []byte("old-generation-ecdf")
	sum := sha256.Sum256(body)
	_, err = publisher.ExecContext(context.Background(), `
		INSERT INTO ecdf (service_id, indicator_id, version, body, bytes, sha256, interval_end)
		VALUES ($1,$2,1,$3,$4,$5,$6)
	`, service.Id, indicatorID, body, len(body), hex.EncodeToString(sum[:]), time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, publisher.QueryRowContext(context.Background(),
		`SELECT pg_advisory_unlock($1,$2)`, service.Id, indicatorID).Scan(&locked))
	require.True(t, locked)
	require.NoError(t, <-updateDone)

	var remaining int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM ecdf WHERE service_id=$1`, service.Id).Scan(&remaining))
	require.Zero(t, remaining, "material update left an old-generation ECDF published")
}

// this tests that the connect function returns a non-nil value.
func TestNewDatabaseConfig(t *testing.T) {
	newDatabaseConfig(t)
}

func newDatabaseConfig(t *testing.T) Config {
	conn := os.Getenv("DATABASE_URL")
	if conn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	db, err := sql.Open("pgx", conn)
	if err != nil {
		t.Fatal(err)
	}
	config := NewDatabaseConfig(db)
	require.NotNil(t, config)
	return config
}

// this test test the get/set config functions along side the read/write data source functions.
func TestGetConfigDatabase(t *testing.T) {
	//t.Skip() // comment this out to run the test manually
	config := newDatabaseConfig(t)
	defer config.Close()
	testConfigFunctions(t, config)
	testDataSourceFunctions(t, config)
	testServiceFunctions(t, config)
}
