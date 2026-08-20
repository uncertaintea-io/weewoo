// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

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

	loadSamples := ecdf.CountSamples(loads)
	latencySamples := ecdf.CountSamples(latencies)

	start := time.Unix(1_700_000_000, 0)
	for i := 0; i < 2; i++ {
		timestamp := start.Add(time.Duration(i) * time.Minute)
		chunk, err := ecdf.Encode(timestamp, loadSamples, latencySamples)
		require.NoError(t, err)
		require.NoError(t, chunkStore.WriteChunk(serviceID, indicatorID, 1, timestamp, chunk))
	}

	var jointECDF bytes.Buffer
	require.NoError(t, ecdf.BuildJointECDF(ctx, chunkStore, serviceID, indicatorID, 1, &jointECDF))

	xs, ps, err := ecdf.Query(ctx, jointECDF.Bytes(), fixedLoad)
	require.NoError(t, err)
	cdf, err := ecdf.LinearInterpolation(xs, ps)
	require.NoError(t, err)

	result := OneSample(cdf, latencySamples)
	require.GreaterOrEqual(t, result.PValue, significanceLevel, "expected sample to be consistent with reference CDF")
}

func TestOneSampleReturnsSmallerPValueForLargerDifference(t *testing.T) {
	uniformCDF := func(value float64) float64 { return value }
	matchingSample := []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0}
	shiftedSample := []float64{0.1, 0.25, 0.35, 0.45, 0.55, 0.65, 0.75, 0.85, 0.95, 1.0}

	matching := OneSample(uniformCDF, ecdf.CountSamples(matchingSample))
	shifted := OneSample(uniformCDF, ecdf.CountSamples(shiftedSample))

	require.Greater(t, matching.PValue, shifted.PValue)
}
