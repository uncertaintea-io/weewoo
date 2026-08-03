package collection

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

const (
	livePrometheusURL = "http://pc0:9090"
	liveLatencyQuery = `sum by (app) (weewoo_http_request_duration_seconds{app="weewoo"})`
	liveLoadQuery    = `irate(histogram_count(sum by (app) (weewoo_http_request_duration_seconds{app="weewoo"}))[30s:])`
)

func TestLiveLatencySamples(t *testing.T) {
	// This test is intended to be run manually, not as part of the automated test suite.
	// It requires a running Prometheus instance with known data.
	t.Skip("manual test")

	ctx := context.Background()
	httpClient := &http.Client{}
	end := time.Now().Truncate(time.Minute)
	start := end.Add(-5 * time.Minute)
	step := time.Minute

	samples, err := QueryPrometheusRangeSamples(ctx, httpClient, livePrometheusURL, liveLatencyQuery, start, end, step)
	require.NoError(t, err)
	assert.NotEmpty(t, samples)

	assert.True(t, slices.IsSortedFunc(samples, func(a, b ecdf.Sample) int {
		return cmp.Compare(a.Value, b.Value)
	}), "samples are not sorted by value")

	for _, sample := range samples {
		t.Logf("Value: %f, Count: %d", sample.Value, sample.Count)
		assert.Greater(t, sample.Count, uint64(0), "value %g has a zero count", sample.Value)
	}
}

func TestLiveLoadSamples(t *testing.T) {
	// This test is intended to be run manually, not as part of the automated test suite.
	// It requires a running Prometheus instance with known data.
	t.Skip("manual test")

	const baseURL = "http://pc0:9090"
	const promQL = `irate(histogram_count(sum by (app) (weewoo_http_request_duration_seconds{app="weewoo"}))[30s:])`

	ctx := context.Background()
	httpClient := &http.Client{}
	end := time.Now().Truncate(time.Minute)
	start := end.Add(-5 * time.Minute)
	step := time.Minute

	samples, err := QueryPrometheusRangeSamples(ctx, httpClient, livePrometheusURL, liveLoadQuery, start, end, step)
	require.NoError(t, err)
	assert.NotEmpty(t, samples)

	assert.True(t, slices.IsSortedFunc(samples, func(a, b ecdf.Sample) int {
		return cmp.Compare(a.Value, b.Value)
	}), "samples are not sorted by value")

	for _, sample := range samples {
		t.Logf("Value: %f, Count: %d", sample.Value, sample.Count)
		assert.Greater(t, sample.Count, uint64(0), "value %g has a zero count", sample.Value)
	}
}

func TestLiveLoadPoints(t *testing.T) {
	// This test is intended to be run manually, not as part of the automated test suite.
	// It requires a running Prometheus instance with known data.
	t.Skip("manual test")

	ctx := context.Background()
	httpClient := &http.Client{}
	end := time.Now().Truncate(time.Minute)
	start := end.Add(-5 * time.Minute)
	step := time.Minute

	points, err := QueryPrometheusRangePoints(ctx, httpClient, livePrometheusURL, liveLoadQuery, start, end, step)
	require.NoError(t, err)
	require.NotEmpty(t, points)

	assert.True(t, slices.IsSortedFunc(points, func(a, b PrometheusPoint) int {
		return a.Timestamp.Compare(b.Timestamp)
	}), "points are not sorted by timestamp")
	assert.True(t, points[0].Timestamp.After(start), "first point overlaps with previous window")

	for _, point := range points {
		t.Logf("Timestamp: %v, Value: %f", point.Timestamp, point.Value)
	}
}
