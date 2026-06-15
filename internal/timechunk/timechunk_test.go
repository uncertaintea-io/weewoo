package timechunk

import (
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
