package collection

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/ecdf"
	"github.com/uncertaintea-io/weewoo/internal/kstests"
)

const (
	analyseSampleTimeout = 5 * time.Second
	analyseSampleAlpha   = 0.001
)

// AnalyseSample evaluates a collected chunk against the current published joint
// ECDF. It returns true when the sample appears anomalous.
func AnalyseSample(chunkStore ecdf.ChunkStore, jointStore ecdf.JointStore, serviceID int, indicatorID int, timestamp time.Time) (bool, error) {
	if chunkStore == nil {
		return false, fmt.Errorf("nil chunk store")
	}
	if jointStore == nil {
		return false, fmt.Errorf("nil joint ECDF store")
	}

	chunk, err := chunkStore.ReadChunk(serviceID, indicatorID, timestamp)
	if err != nil {
		return false, err
	}

	_, loads, latencies, err := ecdf.Decode(chunk)
	if err != nil {
		return false, fmt.Errorf("failed to decode chunk: %w", err)
	}
	if len(loads) == 0 {
		return false, fmt.Errorf("chunk has no load samples")
	}
	if len(latencies) == 0 {
		return false, fmt.Errorf("chunk has no latency samples")
	}

	ctx, cancel := context.WithTimeout(context.Background(), analyseSampleTimeout)
	defer cancel()

	jointECDF, err := jointStore.ReadCurrent(ctx, serviceID, indicatorID)
	if err != nil {
		return false, fmt.Errorf("failed to read current joint ECDF: %w", err)
	}

	loadValue := weightedMean(loads)
	cdf, err := ecdf.Query(ctx, jointECDF, loadValue)
	if err != nil {
		return false, fmt.Errorf("failed to query joint ECDF: %w", err)
	}

	latencySample := expandSamples(latencies)
	probability := kstests.KsTest(cdf, latencySample)
	anomalyScore := 1.0 - probability
	anomalous := anomalyScore > analyseSampleAlpha

	slog.Info(
		"KS test result",
		"anomalous", anomalous,
		"probability", probability,
		"anomaly_score", anomalyScore,
		"samples", len(latencySample),
		"load", loadValue,
	)

	return anomalous, nil
}

// weightedMean calculates the weighted mean of a slice of samples.
func weightedMean(samples []ecdf.Sample) float64 {
	var total float64
	var count uint64
	for _, sample := range samples {
		total += sample.Value * float64(sample.Count)
		count += sample.Count
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

// expandSamples expands a slice of samples into a slice of floats.
func expandSamples(samples []ecdf.Sample) []float64 {
	size := 0
	for _, sample := range samples {
		size += int(sample.Count)
	}

	expanded := make([]float64, 0, size)
	for _, sample := range samples {
		for i := uint64(0); i < sample.Count; i++ {
			expanded = append(expanded, sample.Value)
		}
	}
	return expanded
}
