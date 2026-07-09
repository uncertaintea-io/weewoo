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
	err := chunkStore.WriteChunk(1, 1, t1, chunk1)
	require.NoError(t, err)
	err = chunkStore.WriteChunk(1, 1, t2, chunk2)
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

	err = chunkStore.ScanGoodChunks(context.Background(), 1, 1, out)
	assert.NoError(t, err)
	close(out)
	<-done
	assert.Equal(t, 2, len(scannedChunks))
	assert.Equal(t, chunk1, scannedChunks[0])
	assert.Equal(t, chunk2, scannedChunks[1])

}
