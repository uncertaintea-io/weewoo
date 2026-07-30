package kstests

import (
	"bytes"
	"context"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

func TestOneSampleAgainstJointECDF(t *testing.T) {
	const (
		serviceID         = 1
		indicatorID       = 1
		fixedLoad         = 1.5
		significanceLevel = 0.01
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
			ecdf.CountSamples(loads),
			ecdf.CountSamples(latencies),
		)
		require.NoError(t, err)
		require.NoError(t, chunkStore.WriteChunk(serviceID, indicatorID, 1, timestamp, chunk))
	}

	var jointECDF bytes.Buffer
	require.NoError(t, ecdf.BuildJointECDF(chunkStore, serviceID, indicatorID, &jointECDF))

	cdf, err := ecdf.Query(ctx, jointECDF.Bytes(), fixedLoad)
	require.NoError(t, err)

	result := OneSample(cdf, latencies)
	require.GreaterOrEqual(t, result.PValue, significanceLevel, "expected sample to be consistent with reference CDF")
}

func TestOneSampleReturnsSmallerPValueForLargerDifference(t *testing.T) {
	uniformCDF := func(value float64) float64 { return value }
	matchingSample := []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0}
	shiftedSample := []float64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}

	matching := OneSample(uniformCDF, matchingSample)
	shifted := OneSample(uniformCDF, shiftedSample)

	require.Greater(t, matching.PValue, shifted.PValue)
	require.Less(t, shifted.PValue, 0.01)
}

func TestOneSampleIterMatchesExpandedSample(t *testing.T) {
	cdf := func(value float64) float64 { return value / 10 }
	expanded := []float64{1, 1, 1, 4, 4, 8}
	counted := func(yield func(float64, uint64) bool) {
		for _, sample := range []struct {
			value float64
			count uint64
		}{{1, 3}, {4, 2}, {8, 1}} {
			if !yield(sample.value, sample.count) {
				return
			}
		}
	}

	require.Equal(t, OneSample(cdf, expanded), OneSampleIter(cdf, 6, counted))
}

func TestOneSampleIterEvaluatesEachDistinctValueOnce(t *testing.T) {
	calls := 0
	cdf := func(float64) float64 {
		calls++
		return 0.5
	}
	counted := func(yield func(float64, uint64) bool) {
		yield(1, 1_000_000)
	}

	OneSampleIter(cdf, 1_000_000, counted)
	require.Equal(t, 1, calls)
}
