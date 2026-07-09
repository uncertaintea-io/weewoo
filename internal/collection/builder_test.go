package collection

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

func TestBuildJointECDF(t *testing.T) {
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
	require.NoError(t, chunkStore.WriteChunk(1, LoadLatencyIndicator, start.Add(time.Minute), chunk))

	out, err := ecdf.BuildJointECDFContext(context.Background(), chunkStore, 1, LoadLatencyIndicator, start, end)
	require.NoError(t, err)
	assert.Equal(t, "fake-ecdf-output", string(out))
}

func TestBuildECDFUsesContext(t *testing.T) {
	jecdfPath := filepath.Join(t.TempDir(), "jecdf")
	err := os.WriteFile(jecdfPath, []byte(`#!/bin/sh
sleep 10
`), 0755)
	require.NoError(t, err)

	oldJECDF := flag.Lookup("jecdf").Value.String()
	require.NoError(t, flag.Set("jecdf", jecdfPath))
	t.Cleanup(func() {
		require.NoError(t, flag.Set("jecdf", oldJECDF))
	})

	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	chunk, err := ecdf.Encode(start.Add(time.Minute), []ecdf.Sample{{Value: 1, Count: 1}}, []ecdf.Sample{{Value: 2, Count: 1}})
	require.NoError(t, err)

	chunkStore := ecdf.NewFakeChunkStore()
	require.NoError(t, chunkStore.WriteChunk(1, LoadLatencyIndicator, start.Add(time.Minute), chunk))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = ecdf.BuildJointECDFContext(ctx, chunkStore, 1, LoadLatencyIndicator, start, start.Add(time.Hour))
	require.ErrorIs(t, err, context.Canceled)
}

func TestStartECDFBuilderBuildsConfiguredServices(t *testing.T) {
	jecdfPath := filepath.Join(t.TempDir(), "jecdf")
	err := os.WriteFile(jecdfPath, []byte(`#!/bin/sh
if [ "$1" != "build" ]; then
	exit 2
fi
echo -n 'fake-ecdf-output' > "$0.stdout"
`), 0755)
	require.NoError(t, err)

	oldJECDF := flag.Lookup("jecdf").Value.String()
	require.NoError(t, flag.Set("jecdf", jecdfPath))
	t.Cleanup(func() {
		require.NoError(t, flag.Set("jecdf", oldJECDF))
	})

	cfg := config.NewFakeConfig()
	require.NoError(t, cfg.WriteService(&config.Service{Id: 7, Name: "api"}))

	chunkStore := ecdf.NewFakeChunkStore()
	chunkTime := time.Now().Add(-30 * time.Minute)
	chunk, err := ecdf.Encode(chunkTime, []ecdf.Sample{{Value: 1, Count: 1}}, []ecdf.Sample{{Value: 2, Count: 1}})
	require.NoError(t, err)
	require.NoError(t, chunkStore.WriteChunk(7, LoadLatencyIndicator, chunkTime, chunk))

	scheduler := NewIntervalScheduler(WithSchedulerEventHandler(nil))
	defer scheduler.Stop()

	require.NoError(t, StartECDFBuilder(chunkStore, cfg, scheduler))

	outputPath := jecdfPath + ".stdout"
	require.Eventually(t, func() bool {
		got, err := os.ReadFile(outputPath)
		return err == nil && string(got) == "fake-ecdf-output"
	}, time.Second, 10*time.Millisecond)
}
