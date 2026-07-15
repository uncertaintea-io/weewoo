package kstests

import (
	"bytes"
	"context"
	"flag"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

func TestKsTest(t *testing.T) {
	const (
		serviceID   = 1
		indicatorID = 1
		fixedLoad   = 1.5
		alpha       = 0.05
	)

	jecdfPath := "../../jecdf"
	oldJECDFPath := flag.Lookup("jecdf").Value.String()
	require.NoError(t, flag.Set("jecdf", jecdfPath))
	t.Cleanup(func() {
		_ = flag.Set("jecdf", oldJECDFPath)
	})

	info, err := os.Stat(jecdfPath)
	if err != nil || info.IsDir() || info.Mode().Perm()&0111 == 0 {
		t.Skip("jecdf tool not found or not executable, skipping test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	chunkStore := ecdf.NewFakeChunkStore()
	loads := []float64{1.1, 1.11, 1.12}
	latencies := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	start := time.Unix(1_700_000_000, 0)
	for i := 0; i < 2; i++ {
		timestamp := start.Add(time.Duration(i) * time.Minute)
		chunk, err := ecdf.Encode(
			timestamp,
			ecdf.CountSamples(append([]float64(nil), loads...)),
			ecdf.CountSamples(append([]float64(nil), latencies...)),
		)
		require.NoError(t, err)
		require.NoError(t, chunkStore.WriteChunk(serviceID, indicatorID, timestamp, chunk))
	}

	var jointECDF bytes.Buffer
	require.NoError(t, ecdf.BuildJointECDF(chunkStore, serviceID, indicatorID, &jointECDF))

	cdf, err := ecdf.Query(ctx, jointECDF.Bytes(), fixedLoad)
	require.NoError(t, err)

	sample := append([]float64(nil), latencies...)
	result := KsTest(t, cdf, sample, alpha)
	slog.Info("KS test result", "passed", result, "samples", len(sample), "load", fixedLoad)
	require.True(t, result, "expected sample to match queried ECDF")
}
