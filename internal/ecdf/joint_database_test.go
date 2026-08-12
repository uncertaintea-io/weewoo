package ecdf

import (
	"context"
	"database/sql"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestSQLitePublishAllowsBuildToReadFromSharedDatabase(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/weewoo.db")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TABLE ecdf (
			service_id INTEGER NOT NULL,
			indicator_id INTEGER NOT NULL,
			version INTEGER NOT NULL,
			interval_end TIMESTAMP,
			body BLOB NOT NULL,
			bytes INTEGER NOT NULL,
			sha256 TEXT NOT NULL,
			PRIMARY KEY (service_id, indicator_id, version)
		);
		CREATE UNIQUE INDEX ecdf_build_interval_idx
			ON ecdf (service_id, indicator_id, interval_end)
			WHERE interval_end IS NOT NULL
	`)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	bytesWritten, published, err := NewDatabaseJointStore(db).Publish(ctx, 1, 1, time.Now().UTC(), func(out io.Writer) error {
		var value int
		if err := db.QueryRowContext(ctx, `SELECT 1`).Scan(&value); err != nil {
			return err
		}
		_, err := io.WriteString(out, "joint-ecdf")
		return err
	})

	require.NoError(t, err)
	assert.True(t, published)
	assert.EqualValues(t, len("joint-ecdf"), bytesWritten)
}

func TestSQLiteConcurrentPublishSkipsDuplicateInterval(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/weewoo.db")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TABLE ecdf (
			service_id INTEGER NOT NULL,
			indicator_id INTEGER NOT NULL,
			version INTEGER NOT NULL,
			interval_end TIMESTAMP,
			body BLOB NOT NULL,
			bytes INTEGER NOT NULL,
			sha256 TEXT NOT NULL,
			PRIMARY KEY (service_id, indicator_id, version)
		);
		CREATE UNIQUE INDEX ecdf_build_interval_idx
			ON ecdf (service_id, indicator_id, interval_end)
			WHERE interval_end IS NOT NULL
	`)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	store := NewDatabaseJointStore(db)
	intervalEnd := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	type result struct {
		published bool
		err       error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			_, published, err := store.Publish(ctx, 1, 1, intervalEnd, func(out io.Writer) error {
				entered <- struct{}{}
				<-release
				_, err := io.WriteString(out, "joint-ecdf")
				return err
			})
			results <- result{published: published, err: err}
		}()
	}
	<-entered
	<-entered
	close(release)

	first := <-results
	second := <-results
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	assert.NotEqual(t, first.published, second.published)
}
