// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

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

// QueryPrometheusSamples returns every (value, count) pair from a single-series range query.
// The results are adjusted to exclude any sample at the start boundary, as this will overlap with the previous window's end boundary.
func QueryPrometheusSamples(ctx context.Context, httpClient *http.Client, baseURL, promQL string, start, end time.Time, step time.Duration) ([]ecdf.Sample, error) {
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
		samples, err := getSamplesFromHistograms(stream.Histograms)
		if err != nil {
			return nil, err
		}
		if len(samples) == 0 {
			return nil, ErrNoPrometheusData
		}
		return samples, nil
	}
	if len(stream.Values) > 0 {
		values := stream.Values
		if values[0].Timestamp.Time().Equal(start) {
			values = values[1:]
		}
		if len(values) > 0 {
			return getSamplesFromValues(values)
		}
	}
	return nil, ErrNoPrometheusData
}

type bucketCount struct {
	upper float64
	count uint64
}

func getBucketCountsFromHistogram(histogram *model.SampleHistogram) ([]bucketCount, error) {
	out := make([]bucketCount, len(histogram.Buckets))
	for i, bucket := range histogram.Buckets {
		upper := float64(bucket.Upper)
		if math.IsNaN(upper) || math.IsInf(upper, -1) {
			return nil, fmt.Errorf("query returned invalid bucket upper bound: %v", upper)
		}
		count := uint64(math.Round(float64(bucket.Count)))
		out[i] = bucketCount{upper: upper, count: count}
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
	totalCount := make([]bucketCount, 0, len(last))
	for _, pair := range histograms[1:] {
		current, err := getBucketCountsFromHistogram(pair.Histogram)
		if err != nil {
			return nil, err
		}

		// This assumes that the buckets returned by Prometheus are sorted by upper bound.
		// We do not assume that they have the same buckets, but it is highly likely that they will.
		// We will only accumulate counts for the buckets that are present in both histograms.
		// Histogram resets are handled by only accumulating increases that are greater than zero.
		for lastIndex, currentIndex, totalIndex := 0, 0, 0; lastIndex < len(last) && currentIndex < len(current); {
			switch {
			case last[lastIndex].upper < current[currentIndex].upper:
				lastIndex++
			case current[currentIndex].upper < last[lastIndex].upper:
				currentIndex++
			default:
				if current[currentIndex].count > last[lastIndex].count {
					increase := current[currentIndex].count - last[lastIndex].count
					upper := current[currentIndex].upper
					for totalIndex < len(totalCount) && totalCount[totalIndex].upper < upper {
						totalIndex++
					}
					if totalIndex < len(totalCount) && totalCount[totalIndex].upper == upper {
						totalCount[totalIndex].count += increase
					} else {
						totalCount = slices.Insert(totalCount, totalIndex, bucketCount{upper: upper, count: increase})
					}
					totalIndex++
				}
				lastIndex++
				currentIndex++
			}
		}
		last = current
	}

	samples := make([]ecdf.Sample, 0, len(totalCount))
	for _, bucket := range totalCount {
		if bucket.count > 0 {
			if math.IsInf(bucket.upper, +1) {
				slog.Warn("query dropped samples from overflow histogram bucket", slog.Uint64("count", bucket.count))
				continue
			}
			samples = append(samples, ecdf.Sample{
				Value: bucket.upper,
				Count: bucket.count,
			})
		}
	}
	return samples, nil
}

func getSamplesFromValues(values []model.SamplePair) ([]ecdf.Sample, error) {
	samples := make([]ecdf.Sample, 0, len(values))
	for _, pair := range values {
		value := float64(pair.Value)
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("query returned non-finite sample: %v", value)
		}
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
