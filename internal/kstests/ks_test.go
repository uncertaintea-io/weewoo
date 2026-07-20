package kstests

import (
	"bytes"
	"context"
	"flag"
	"log/slog"
	"math"
	"math/rand/v2"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

func TestKsTest(t *testing.T) {
	const (
		serviceID   = 1
		indicatorID = 1
		fixedLoad   = 1.5
		alpha       = 0.001
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
		require.NoError(t, chunkStore.WriteChunk(serviceID, indicatorID, timestamp, chunk))
	}

	var jointECDF bytes.Buffer
	require.NoError(t, ecdf.BuildJointECDF(chunkStore, serviceID, indicatorID, &jointECDF))

	cdf, err := ecdf.Query(ctx, jointECDF.Bytes(), fixedLoad)
	require.NoError(t, err)

	p := KsTest(cdf, latencies)
	t.Logf("Probability sample was drawn from distribution: %f", p)
	p = 1.0 - p
	passed := p <= alpha
	slog.Info("KS test result", "passed", passed, "samples", len(latencies), "load", fixedLoad)
	assert.True(t, passed, "expected sample to match queried ECDF with p-value of %f, was %f", alpha, p)
}

func TestKsTestIterMatchesExpandedSample(t *testing.T) {
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

	require.Equal(t, KsTest(cdf, expanded), KsTestIter(cdf, 6, counted))
}

func TestKsTestIterEvaluatesEachDistinctValueOnce(t *testing.T) {
	calls := 0
	cdf := func(float64) float64 {
		calls++
		return 0.5
	}
	counted := func(yield func(float64, uint64) bool) {
		yield(1, 1_000_000)
	}

	KsTestIter(cdf, 1_000_000, counted)
	require.Equal(t, 1, calls)
}

func TestKsTestIterMatchesReferenceImplementation(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	cdf := func(value float64) float64 {
		return 1 / (1 + math.Exp(-value))
	}

	for run := 0; run < 1_000; run++ {
		records := make([]struct {
			value float64
			count uint64
		}, rng.IntN(20))
		var expanded []float64
		var count uint64
		for i := range records {
			records[i].value = float64(rng.IntN(21)-10) / 2
			records[i].count = uint64(rng.IntN(8))
			count += records[i].count
			for range records[i].count {
				expanded = append(expanded, records[i].value)
			}
		}
		slices.SortFunc(records, func(a, b struct {
			value float64
			count uint64
		}) int {
			if a.value < b.value {
				return -1
			}
			if a.value > b.value {
				return 1
			}
			return 0
		})
		counted := func(yield func(float64, uint64) bool) {
			for _, record := range records {
				if !yield(record.value, record.count) {
					return
				}
			}
		}

		require.Equal(t, referenceKsTest(cdf, expanded), KsTestIter(cdf, count, counted), "run %d", run)
	}
}

func referenceKsTest(cdf func(float64) float64, sample []float64) float64 {
	slices.Sort(sample)
	n := float64(len(sample))
	maxDifference := 0.0
	for i, value := range sample {
		p := cdf(value)
		maxDifference = math.Max(maxDifference, math.Abs(p-float64(i)/n))
		maxDifference = math.Max(maxDifference, math.Abs(float64(i+1)/n-p))
	}
	return kprob(maxDifference * math.Sqrt(n))
}
