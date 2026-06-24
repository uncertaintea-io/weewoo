package ecdf

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFakeChunkStore(t *testing.T) {
	chunkStore := NewFakeChunkStore()
	require.NotNil(t, chunkStore)
}

func TestFakeChunkStoreFunctions(t *testing.T) {
	chunkStore := NewFakeChunkStore()
	require.NotNil(t, chunkStore)

	timestamp := time.Unix(1781561298, 0)
	x := []Sample{{Value: 1, Count: 1}}
	y := []Sample{{Value: 2, Count: 1}}

	// testing writeChunk
	t.Run("WriteChunk", func(t *testing.T) {
		err := chunkStore.WriteChunk(1, 1, timestamp, x, y)
		require.NoError(t, err)
	})

	// testing readChunk
	t.Run("ReadChunk", func(t *testing.T) {
		chunk, err := chunkStore.ReadChunk(1, 1, timestamp)
		require.NoError(t, err)
		require.Equal(t, timestamp, chunk.Timestamp)
		require.Equal(t, x, chunk.X)
		require.Equal(t, y, chunk.Y)
	})

	t.Run("ReadMissingChunk", func(t *testing.T) {
		chunk, err := chunkStore.ReadChunk(1, 1, timestamp.Add(time.Second))
		require.EqualError(t, err, ChunkNotFoundError.Error())
		require.Empty(t, chunk)
	})
}
