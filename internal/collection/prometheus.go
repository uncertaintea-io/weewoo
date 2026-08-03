package collection

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"

	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

var ErrNoPrometheusData = errors.New("no data returned")

type PrometheusPoint struct {
	Timestamp time.Time
	Value     float64
}

// QueryPrometheusRangeSamples returns every (value, count) pair from a single-series range query.
func QueryPrometheusRangeSamples(ctx context.Context, httpClient *http.Client, baseURL, promQL string, start, end time.Time, step time.Duration) ([]ecdf.Sample, error) {
	client, err := newPrometheusClient(baseURL, httpClient)
	if err != nil {
		return nil, fmt.Errorf("creating Prometheus client: %w", err)
	}
	queryRange := v1.Range{
		Start: start,
		End:   end,
		Step:  step,
	}
	result, warnings, err := v1.NewAPI(client).QueryRange(
		ctx,
		promQL,
		queryRange,
	)
	if err != nil {
		return nil, fmt.Errorf("query Prometheus: %w", err)
	}
	for _, warning := range warnings {
		slog.Warn(warning)
	}

	// Inspect the value to ensure it's a range matrix.
	matrix, ok := result.(model.Matrix)
	if !ok {
		return nil, fmt.Errorf("query returned %s, expected a range matrix", result.Type())
	}
	switch len(matrix) {
	case 0:
		return nil, ErrNoPrometheusData
	case 1:
		// Expected case.
	default:
		return nil, fmt.Errorf("unexpected 1 time series, got %d", len(matrix))
	}
	stream := matrix[0]
	if len(stream.Histograms) > 0 {
		return getSamplesFromHistograms(stream.Histograms)
	}
	if len(stream.Values) > 0 {
		return getSamplesFromValues(stream.Values)
	}
	return nil, ErrNoPrometheusData
}

func getBucketCountsFromHistogram(histogram *model.SampleHistogram) (map[float64]uint64, error) {
	out := make(map[float64]uint64, len(histogram.Buckets))
	for _, bucket := range histogram.Buckets {
		upper := float64(bucket.Upper)
		if _, exists := out[upper]; exists {
			return nil, fmt.Errorf("duplicate bucket upper bound: %f", upper)
		}
		count := uint64(math.Round(float64(bucket.Count)))
		out[upper] = count
	}
	return out, nil
}

func getSamplesFromHistograms(histograms []model.SampleHistogramPair) ([]ecdf.Sample, error) {
	n := len(histograms)
	if n < 2 {
		return nil, fmt.Errorf("not enough histogram samples to compute rate: got %d, need at least 2", len(histograms))
	}

	// Get the bucket counts from the first histogram in the results
	last, err := getBucketCountsFromHistogram(histograms[0].Histogram)
	if err != nil {
		return nil, err
	}

	// For each subsequent histogram, compute the difference in counts for each bucket and accumulate the total counts.
	// The count can go down if the histogram is reset, so we only accumulate counts that are greater than the last count.
	totalCount := make(map[float64]uint64)
	for _, pair := range histograms[1:] {
		current, err := getBucketCountsFromHistogram(pair.Histogram)
		if err != nil {
			return nil, err
		}
		for upper, currentCount := range current {
			lastCount := last[upper]
			if currentCount < lastCount {
				break // Histogram reset, skip this bucket for this sample
			}
			totalCount[upper] = totalCount[upper] + currentCount - lastCount
		}
		last = current
	}

	samples := make([]ecdf.Sample, 0, len(last))
	for upper, count := range totalCount {
		samples = append(samples, ecdf.Sample{
			Value: float64(upper),
			Count: count,
		})
	}
	return samples, nil
}

func getSamplesFromValues(values []model.SamplePair) ([]ecdf.Sample, error) {
	samples := make([]ecdf.Sample, 0, len(values))
	for _, pair := range values {
		value := float64(pair.Value)
		i, found := slices.BinarySearchFunc(samples, value, func(sample ecdf.Sample, value float64) int {
			return cmp.Compare(sample.Value, value)
		})
		if found {
			samples[i].Count++
		} else {
			samples = slices.Insert(samples, i, ecdf.Sample{
				Value: value,
				Count: 1,
			})
		}
	}
	return samples, nil
}

func QueryPrometheusRangePoints(ctx context.Context, httpClient *http.Client, baseURL, promQL string, start, end time.Time, step time.Duration) ([]PrometheusPoint, error) {
	if step <= 0 || step%time.Second != 0 {
		return nil, fmt.Errorf("Prometheus range step must be a positive whole number of seconds")
	}

	client, err := newPrometheusClient(baseURL, httpClient)
	if err != nil {
		return nil, fmt.Errorf("creating Prometheus client: %w", err)
	}
	queryRange := v1.Range{
		Start: start,
		End:   end,
		Step:  step,
	}
	result, warnings, err := v1.NewAPI(client).QueryRange(
		ctx,
		promQL,
		queryRange,
	)
	if err != nil {
		return nil, fmt.Errorf("query Prometheus: %w", err)
	}
	for _, warning := range warnings {
		slog.Warn(warning)
	}

	// Inspect the value to ensure it's a range matrix.
	matrix, ok := result.(model.Matrix)
	if !ok {
		return nil, fmt.Errorf("query returned %s, expected a range matrix", result.Type())
	}
	switch len(matrix) {
	case 0:
		return nil, ErrNoPrometheusData
	case 1:
		// Expected case.
	default:
		return nil, fmt.Errorf("unexpected 1 time series, got %d", len(matrix))
	}
	stream := matrix[0]
	if len(stream.Histograms) > 0 {
		return nil, fmt.Errorf("expected value query, got histograms")
	}
	if len(stream.Values) == 0 {
		return nil, ErrNoPrometheusData
	}
	points := make([]PrometheusPoint, len(stream.Values))
	for i, sample := range stream.Values {
		if math.IsNaN(float64(sample.Value)) || math.IsInf(float64(sample.Value), 0) {
			return nil, fmt.Errorf("query returned invalid sample value: %v", sample.Value)
		}
		points[i] = PrometheusPoint{
			Timestamp: sample.Timestamp.Time(),
			Value:     float64(sample.Value),
		}
	}
	return points, nil
}

func newPrometheusClient(baseURL string, httpClient *http.Client) (api.Client, error) {
	config := api.Config{Address: baseURL}
	if httpClient != nil {
		config.RoundTripper = httpClient.Transport
		if config.RoundTripper == nil {
			config.RoundTripper = http.DefaultTransport
		}
	}
	return api.NewClient(config)
}
