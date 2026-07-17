package collection

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/alerting"
	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
	"github.com/uncertaintea-io/weewoo/internal/kstests"
)

const (
	analyseSampleTimeout = 5 * time.Second
	analyseSampleAlpha   = 0.001
	maxExpandedSamples   = 1_000_000
)

// AnalyseSample evaluates a collected chunk against the current published joint
// ECDF. It returns true when the sample appears anomalous.
func AnalyseSample(chunkStore ecdf.ChunkStore, cfg config.Config, jointStore ecdf.JointStore, serviceID int, indicatorID int, timestamp time.Time) (bool, error) {
	if chunkStore == nil {
		return false, fmt.Errorf("nil chunk store")
	}
	if cfg == nil {
		return false, fmt.Errorf("nil config")
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
	if sampleCount(loads, 0) == 0 {
		return false, fmt.Errorf("chunk has no load observations")
	}
	latencyCount, err := checkedSampleCount(latencies, maxExpandedSamples)
	if err != nil {
		return false, fmt.Errorf("invalid latency samples: %w", err)
	}
	if latencyCount == 0 {
		return false, fmt.Errorf("chunk has no latency observations")
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

	latencySample := expandSamples(latencies, latencyCount)
	probability := kstests.KsTest(cdf, latencySample)
	anomalyScore := 1.0 - probability
	anomalous := anomalyScore > analyseSampleAlpha
	if anomalous {
		generatorURL, err := cfg.GetConfig("alert_generator_url")
		if err != nil {
			return false, fmt.Errorf("failed to read alert generator URL: %w", err)
		}
		if err := alerting.SendItContext(ctx, cfg, alerting.AlertingOptions{
			Service:      strconv.Itoa(serviceID),
			Serverity:    "critical",
			Indicator:    strconv.Itoa(indicatorID),
			AlertName:    "anomalous_sample",
			Summary:      "Anomalous sample detected",
			Description:  fmt.Sprintf("Anomalous sample detected for service %d and indicator %d", serviceID, indicatorID),
			Annotations:  nil,
			GeneratorURL: generatorURL,
		}); err != nil {
			slog.Error(
				"failed to send anomaly alert",
				"service_id", serviceID,
				"indicator_id", indicatorID,
				"timestamp", timestamp,
				"error", err,
			)
		}
	}

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
func expandSamples(samples []ecdf.Sample, size int) []float64 {
	expanded := make([]float64, 0, size)
	for _, sample := range samples {
		for i := uint64(0); i < sample.Count; i++ {
			expanded = append(expanded, sample.Value)
		}
	}
	return expanded
}

func sampleCount(samples []ecdf.Sample, stopAfter uint64) uint64 {
	var total uint64
	for _, sample := range samples {
		if ^uint64(0)-total < sample.Count {
			return ^uint64(0)
		}
		total += sample.Count
		if stopAfter > 0 && total > stopAfter {
			return total
		}
	}
	return total
}

func checkedSampleCount(samples []ecdf.Sample, limit int) (int, error) {
	total := sampleCount(samples, uint64(limit))
	if total > uint64(limit) {
		return 0, fmt.Errorf("observation count %d exceeds limit %d", total, limit)
	}
	return int(total), nil
}
