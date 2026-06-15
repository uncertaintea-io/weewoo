package timechunk

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildReadWrite(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		x    []Sample
		y    []Sample
		want []JointSample
	}{
		{
			name: "1*1",
			x:    []Sample{{Value: 1, Count: 1}},
			y:    []Sample{{Value: 2, Count: 1}},
			want: []JointSample{{X: 1, Y: 2, Weight: 1}},
		},
		{
			name: "1*2",
			x:    []Sample{{Value: 1, Count: 1}},
			y:    []Sample{{Value: 2, Count: 1}, {Value: 3, Count: 1}},
			want: []JointSample{{X: 1, Y: 2, Weight: 1}, {X: 1, Y: 3, Weight: 1}},
		},
		{
			name: "2*2",
			x:    []Sample{{Value: 1, Count: 1}, {Value: 2, Count: 1}},
			y:    []Sample{{Value: 3, Count: 1}, {Value: 4, Count: 1}},
			want: []JointSample{
				{X: 1, Y: 3, Weight: 1},
				{X: 1, Y: 4, Weight: 1},
				{X: 2, Y: 3, Weight: 1},
				{X: 2, Y: 4, Weight: 1},
			},
		},
	}
	now := time.Unix(1781469600, 0)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			chunk, err := Build(now, test.x, test.y)
			require.NoError(t, err)
			require.NotNil(t, chunk)
			assert.Equal(t, now, chunk.Timestamp)
			assert.Equal(t, test.want, chunk.Samples)

			buf := bytes.NewBuffer(nil)
			err = chunk.WriteTo(buf)
			require.NoError(t, err)
			require.NotNil(t, buf)

			buf = bytes.NewBuffer(buf.Bytes())
			decoded, err := ReadFrom(buf)
			require.NoError(t, err)
			require.NotNil(t, decoded)

			assert.Equal(t, now, decoded.Timestamp)
			assert.Equal(t, chunk.Samples, decoded.Samples)
		})
	}
}
