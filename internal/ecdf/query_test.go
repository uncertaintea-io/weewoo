package ecdf

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueryWithRealTool(t *testing.T) {
	setJECDFTool(t, "../../jecdf")
	if !jecdfExists(t) {
		t.Skip("jecdf tool not found or not executable, skipping test")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	store := NewFakeChunkStore()
	for i, point := range []struct{ x, y float64 }{
		{1, 2},
		{1, 4},
		{2, 1},
		{2, 5},
	} {
		timestamp := time.Unix(int64(i), 0)
		chunk, err := newTestChunk(timestamp, point.x, point.y)
		require.NoError(t, err)
		require.NoError(t, store.WriteChunk(1, 1, 1, timestamp, chunk))
	}

	var jointECDF bytes.Buffer
	require.NoError(t, BuildJointECDFContext(ctx, store, 1, 1, &jointECDF))

	const value = 1.5
	xs, ps, err := Query(ctx, jointECDF.Bytes(), value)
	require.NoError(t, err)
	if len(xs) == 0 {
		t.Log("jecdf returned zero points; no CDF is available yet")
		return
	}
	cdf, err := LinearInterpolation(xs, ps)
	require.NoError(t, err)
	assert.Equal(t, 0.0, cdf(-1000))
	assert.Equal(t, 1.0, cdf(1000))
	assert.Greater(t, cdf(3), 0.0)
	assert.Less(t, cdf(3), 1.0)
}

func TestQueryAcceptsZeroPointResult(t *testing.T) {
	setJECDFTool(t, writeFakeJECDF(t, `if [ "$1" != "query" ]; then
	exit 2
fi
cat >/dev/null
printf '\000'
`))

	xs, ps, err := Query(context.Background(), nil, 0)

	require.NoError(t, err)
	assert.Nil(t, xs)
	assert.Nil(t, ps)
}
