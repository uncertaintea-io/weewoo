package timechunk

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeDecode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		x    []Sample
		y    []Sample
	}{
		{
			name: "0*0",
			x:    []Sample{},
			y:    []Sample{},
		},
		{
			name: "1*1",
			x:    []Sample{{Value: 1, Count: 1}},
			y:    []Sample{{Value: 2, Count: 1}},
		},
		{
			name: "1*2",
			x:    []Sample{{Value: 1, Count: 1}},
			y:    []Sample{{Value: 2, Count: 1}, {Value: 3, Count: 1}},
		},
		{
			name: "2*2",
			x:    []Sample{{Value: 1, Count: 1}, {Value: 2, Count: 1}},
			y:    []Sample{{Value: 3, Count: 1}, {Value: 4, Count: 1}},
		},
		{
			name: "2*3",
			x:    []Sample{{Value: 1, Count: 2}, {Value: 2, Count: 1}},
			y:    []Sample{{Value: 3, Count: 1}, {Value: 4, Count: 2}, {Value: 5, Count: 3}},
		},
	}
	now := time.Now().Unix()
	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			timestamp := time.Unix(now+int64(i), 0)

			chunk, err := Encode(timestamp, test.x, test.y)
			require.NoError(t, err)
			require.NotNil(t, chunk)
			t.Logf("chunk: %x (len: %d)", chunk, len(chunk))

			decodedTimestamp, decodedX, decodedY, err := Decode(chunk)
			require.NoError(t, err)
			assert.Equal(t, timestamp, decodedTimestamp)
			assert.Equal(t, test.x, decodedX)
			assert.Equal(t, test.y, decodedY)
		})
	}
}

var (
	diskTimestamp = time.Unix(1781561298, 0)
	diskX = []Sample{{Value: 1, Count: 1}, {Value: 2, Count: 2}}
	diskY = []Sample{{Value: 3, Count: 3}, {Value: 4, Count: 4}, {Value: 5, Count: 5}}
)

// The tests below are used to create and validate a "golden" chunk file.
// This file isn't used in the code or these tests, but it is useful for 
// testing compatibility with internal tools written in other languages.

func TestWriteChunkToDisk(t *testing.T) {
	t.Skip("skipping write to disk")
	
	chunk, err := Encode(diskTimestamp, diskX, diskY)
	require.NoError(t, err)
	require.NotNil(t, chunk)
	
	err = os.WriteFile("testdata/chunk.bin", chunk, 0644)
	require.NoError(t, err)
}

func TestReadChunkFromDisk(t *testing.T) {
	t.Skip("skipping read from disk")

	chunk, err := os.ReadFile("testdata/chunk.bin")
	require.NoError(t, err)
	require.NotNil(t, chunk)

	decodedTimestamp, decodedX, decodedY, err := Decode(chunk)
	require.NoError(t, err)
	require.Equal(t, diskTimestamp, decodedTimestamp)
	require.Equal(t, diskX, decodedX)
	require.Equal(t, diskY, decodedY)
}
