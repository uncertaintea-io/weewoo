package collection

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

func TestBuildECDF(t *testing.T) {
	jecdfPath := filepath.Join(t.TempDir(), "jecdf")
	err := os.WriteFile(jecdfPath, []byte(`#!/bin/sh
if [ "$1" != "build" ]; then
	exit 2
fi
cat >/dev/null
echo -n 'fake-ecdf-output'
`), 0755)
	require.NoError(t, err)

	oldJECDF := flag.Lookup("jecdf").Value.String()
	require.NoError(t, flag.Set("jecdf", jecdfPath))
	t.Cleanup(func() {
		require.NoError(t, flag.Set("jecdf", oldJECDF))
	})

	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	chunk, err := ecdf.Encode(start.Add(time.Minute), []ecdf.Sample{{Value: 1, Count: 1}}, []ecdf.Sample{{Value: 2, Count: 1}})
	require.NoError(t, err)

	chunkStore := ecdf.NewFakeChunkStore()
	require.NoError(t, chunkStore.WriteChunk(ecdfBuilderServiceID, LoadLatencyIndicator, start.Add(time.Minute), chunk))

	out, err := BuildECDF(chunkStore, nil, ecdfBuilderServiceID, LoadLatencyIndicator, start, end)
	require.NoError(t, err)
	assert.Equal(t, "fake-ecdf-output", string(out))
}
