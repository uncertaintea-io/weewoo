package ecdf

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

func TestDatabaseChunkStore(t *testing.T) {
	db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	defer db.Close()
	chunkStore := NewDatabaseChunkStore(db)
	require.NotNil(t, chunkStore)
}

func TestDatabaseChunkStoreFunctions(t *testing.T) {
	db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	defer db.Close()
	chunkStore := NewDatabaseChunkStore(db)
	require.NotNil(t, chunkStore)

	// testing writeChunk
	t.Run("WriteChunk", func(t *testing.T) {
		err = chunkStore.WriteChunk(1, 1, diskTimestamp, diskX, diskY)
		require.NoError(t, err)
	})

	// testing readChunk
	t.Run("ReadChunk", func(t *testing.T) {
		readChunk, err := chunkStore.ReadChunk(1, 1, diskTimestamp)
		require.NoError(t, err)
		require.NotNil(t, readChunk)
		require.Equal(t, diskTimestamp, readChunk.Timestamp)
		require.Equal(t, diskX, readChunk.X)
		require.Equal(t, diskY, readChunk.Y)
	})
}
