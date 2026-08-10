package ecdf

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestDatabaseChunkStoreUsesWholeSecondTimestamps(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/weewoo.db")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`
		CREATE TABLE time_chunk (
			service_id INTEGER NOT NULL,
			indicator_id INTEGER NOT NULL,
			"timestamp" TIMESTAMP NOT NULL,
			chunk BLOB,
			collected_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			generation INTEGER NOT NULL DEFAULT 1,
			PRIMARY KEY (service_id, indicator_id, "timestamp")
		);
		CREATE TABLE verdict (
			service_id INTEGER,
			indicator_id INTEGER,
			"timestamp" TIMESTAMP,
			automated_good BOOLEAN,
			pvalue REAL,
			analysis_state TEXT NOT NULL DEFAULT 'pending',
			review_override BOOLEAN,
			reviewed_at TIMESTAMP,
			review_reason TEXT,
			generation INTEGER NOT NULL DEFAULT 1,
			PRIMARY KEY (service_id, indicator_id, "timestamp")
		)
	`)
	require.NoError(t, err)

	store := NewDatabaseChunkStore(db)
	timestamp := time.Date(2026, 8, 10, 12, 0, 0, 456789000, time.UTC)
	require.NoError(t, store.WriteChunk(1, 1, 1, timestamp, []byte("chunk")))

	var stored time.Time
	require.NoError(t, db.QueryRow(`SELECT "timestamp" FROM time_chunk`).Scan(&stored))
	assert.Equal(t, timestamp.Truncate(time.Second), stored)
	chunk, err := store.ReadChunk(1, 1, timestamp)
	require.NoError(t, err)
	assert.Equal(t, []byte("chunk"), chunk)
	require.NoError(t, store.WriteVerdict(context.Background(), 1, 1, 1, timestamp, true, 0.5))

	var state string
	require.NoError(t, db.QueryRow(`SELECT analysis_state FROM verdict`).Scan(&state))
	assert.Equal(t, "good", state)
}

func TestDatabaseChunkStoreFunctions(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL environment variable is not set, skipping database tests")
		return
	}
	db, err := sql.Open("pgx", databaseURL)
	require.NoError(t, err)
	defer db.Close()

	chunkStore := NewDatabaseChunkStore(db)
	require.NotNil(t, chunkStore)

	// testing writeChunk
	now := time.Now()
	dummyChunk := []byte{0x01, 0x02, 0x03}

	t.Run("WriteChunk", func(t *testing.T) {
		err = chunkStore.WriteChunk(1, 1, 1, now, dummyChunk)
		require.NoError(t, err)
	})

	// testing readChunk
	t.Run("ReadChunk", func(t *testing.T) {
		readChunk, err := chunkStore.ReadChunk(1, 1, now)
		require.NoError(t, err)
		assert.Equal(t, dummyChunk, readChunk)
	})
}
