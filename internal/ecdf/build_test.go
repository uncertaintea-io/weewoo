package ecdf

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestChunk(t time.Time, x, y float64) ([]byte, error) {
	return Encode(t, []Sample{{x, 1}}, []Sample{{y, 1}})
}

func TestBuildECDF(t *testing.T) {
	const (
		serviceId   = 1
		indicatorId = 1
	)
	store := NewFakeChunkStore()
	require.NotNil(t, store)
	t2 := time.Now()
	t1 := t2.Add(-time.Minute)

	chunk, err := newTestChunk(t1, 1, 2)
	require.NoError(t, err)
	err = store.WriteChunk(serviceId, indicatorId, t1, chunk)
	require.NoError(t, err)

	chunk2, err := newTestChunk(t2, 2, 1)
	require.NoError(t, err)
	err = store.WriteChunk(serviceId, indicatorId, t2, chunk2)
	require.NoError(t, err)

	cwd, err := os.Getwd()
	require.NoError(t, err)
	t.Logf("Current working directory = %s", cwd)

	out, err := BuildECDF(store, serviceId, indicatorId, t1.Add(-time.Second), t2.Add(time.Second))
	require.NoError(t, err)
	assert.NotNil(t, out)
	assert.GreaterOrEqual(t, len(out), 10)
}
