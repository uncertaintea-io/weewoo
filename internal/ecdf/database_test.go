package ecdf

import (
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatabaseChunkStoreFunctions(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL environment variable is not set, skipping database tests")
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
		err = chunkStore.WriteChunk(1, 1, now, dummyChunk)
		require.NoError(t, err)
	})

	// testing readChunk
	t.Run("ReadChunk", func(t *testing.T) {
		readChunk, err := chunkStore.ReadChunk(1, 1, now)
		require.NoError(t, err)
		assert.Equal(t, dummyChunk, readChunk)
	})
}
