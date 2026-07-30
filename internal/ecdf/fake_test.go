package ecdf

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFakeChunkStoreFunctions(t *testing.T) {
	chunkStore := NewFakeChunkStore()
	require.NotNil(t, chunkStore)

	t2 := time.Now()
	t1 := t2.Add(-time.Minute)
	chunk1 := []byte{0x01, 0x02, 0x03}
	chunk2 := []byte{0x02, 0x04, 0x06}

	// Write chunks
	err := chunkStore.WriteChunk(1, 1, 1, t1, chunk1)
	require.NoError(t, err)
	err = chunkStore.WriteChunk(1, 1, 1, t2, chunk2)
	require.NoError(t, err)

	// Read chunks
	readChunk, err := chunkStore.ReadChunk(1, 1, t1)
	require.NoError(t, err)
	assert.Equal(t, chunk1, readChunk)

	readChunk, err = chunkStore.ReadChunk(1, 1, t2)
	require.NoError(t, err)
	assert.Equal(t, chunk2, readChunk)

	// Scan good chunks
	out := make(chan []byte, 2)
	done := make(chan struct{})
	var scannedChunks [][]byte
	go func() {
		for chunk := range out {
			scannedChunks = append(scannedChunks, chunk)
		}
		done <- struct{}{}
	}()

	err = chunkStore.ScanGoodChunks(context.Background(), 1, 1, 1, out)
	assert.NoError(t, err)
	close(out)
	<-done
	assert.Equal(t, 2, len(scannedChunks))
	assert.Equal(t, chunk1, scannedChunks[0])
	assert.Equal(t, chunk2, scannedChunks[1])

}

func TestFakeChunkStoreVerdictControlsBuildEligibility(t *testing.T) {
	chunkStore := NewFakeChunkStore()
	timestamp := time.Unix(1_700_000_000, 0)
	original := []byte{0x01}
	replacement := []byte{0x02}

	require.NoError(t, chunkStore.WriteChunk(1, 1, 1, timestamp, original))
	require.NoError(t, chunkStore.WriteVerdict(context.Background(), 1, 1, 1, timestamp, false, 0.001))
	require.NoError(t, chunkStore.WriteChunk(1, 1, 1, timestamp, replacement))

	out := make(chan []byte, 1)
	require.NoError(t, chunkStore.ScanGoodChunks(context.Background(), 1, 1, 1, out))
	close(out)
	require.Empty(t, out)

	require.NoError(t, chunkStore.WriteVerdict(context.Background(), 1, 1, 1, timestamp, true, 0.9))
	out = make(chan []byte, 1)
	require.NoError(t, chunkStore.ScanGoodChunks(context.Background(), 1, 1, 1, out))
	close(out)
	require.Equal(t, replacement, <-out)
}

func TestChunkGenerationRejectsLateWritesFromPreviousConfiguration(t *testing.T) {
	store := NewFakeChunkStore()
	timestamp := time.Now().UTC()
	require.NoError(t, store.WriteChunk(1, 1, 1, timestamp, []byte("old generation")))
	require.NoError(t, store.WriteChunk(1, 1, 2, timestamp, []byte("new generation")))
	require.NoError(t, store.WriteChunk(1, 1, 1, timestamp, []byte("late old callback")))

	chunks := make(chan []byte, 1)
	require.NoError(t, store.ScanGoodChunks(context.Background(), 1, 1, 2, chunks))
	close(chunks)

	require.Equal(t, []byte("new generation"), <-chunks)
}
