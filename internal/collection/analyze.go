package collection

import (
	"cmp"
	"context"
	"fmt"
	"iter"
	"log/slog"
	"math/bits"
	"slices"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/alerting"
	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
	"github.com/uncertaintea-io/weewoo/internal/kstests"
)

const (
	analyzeSampleTimeout = 5 * time.Second
	ksSignificanceLevel  = 0.01
)

// analyzeSample evaluates collected samples against the current published joint
// ECDF. It returns true when the samples appear anomalous.
func analyzeSample(ctx context.Context, cfg config.Config, jointStore ecdf.JointStore, alerts AlertQueue, service *config.Service, indicatorID int, timestamp time.Time, loads, latencies []ecdf.Sample) (bool, error) {
	if cfg == nil {
		return false, fmt.Errorf("nil config")
	}
	if jointStore == nil {
		return false, fmt.Errorf("nil joint ECDF store")
	}

	if len(loads) == 0 {
		return false, fmt.Errorf("chunk has no load samples")
	}
	if len(latencies) == 0 {
		return false, fmt.Errorf("chunk has no latency samples")
	}
	loadCount, err := checkedSampleCount(loads)
	if err != nil {
		return false, fmt.Errorf("invalid load samples: %w", err)
	}
	if loadCount == 0 {
		return false, fmt.Errorf("chunk has no load observations")
	}
	latencyCount, err := checkedSampleCount(latencies)
	if err != nil {
		return false, fmt.Errorf("invalid latency samples: %w", err)
	}
	if latencyCount == 0 {
		return false, fmt.Errorf("chunk has no latency observations")
	}

	jointECDF, err := jointStore.ReadCurrent(ctx, service.Id, indicatorID)
	if err != nil {
		return false, fmt.Errorf("failed to read current joint ECDF: %w", err)
	}

	loadValue := weightedMean(loads)
	cdf, err := ecdf.Query(ctx, jointECDF, loadValue)
	if err != nil {
		return false, fmt.Errorf("failed to query joint ECDF: %w", err)
	}
	if cdf == nil {
		slog.Info(
			"skipping sample analysis because no JECDF points are available",
			"service_id", service.Id,
			"indicator_id", indicatorID,
			"timestamp", timestamp,
		)
		return false, nil
	}

	sortedLatencies := slices.Clone(latencies)
	slices.SortFunc(sortedLatencies, func(a, b ecdf.Sample) int {
		return cmp.Compare(a.Value, b.Value)
	})
	latencyValues := func(yield func(float64, uint64) bool) {
		for _, sample := range sortedLatencies {
			if !yield(sample.Value, sample.Count) {
				return
			}
		}
	}
	ksResult := kstests.OneSampleIter(cdf, latencyCount, iter.Seq2[float64, uint64](latencyValues))
	anomalous := isStatisticallySignificant(ksResult.PValue)
	if anomalous {
		generatorURL, err := cfg.GetConfig("alert_generator_url")
		if err != nil {
			return false, fmt.Errorf("failed to read alert generator URL: %w", err)
		}
		if alerts == nil {
			slog.Error(
				"cannot queue anomaly alert",
				"service_id", service.Id,
				"indicator_id", indicatorID,
				"timestamp", timestamp,
				"error", "nil alert queue",
			)
		} else if err := alerts.Submit(alerting.AlertingOptions{
			Service:   service.Name,
			Serverity: "critical",
			Indicator: "Load vs. Latency",
			AlertName: "anomalous_sample",
			Summary:   "Anomalous sample detected",
			Description: fmt.Sprintf(
				"Current latency distribution differs from the reference at load %f (KS p-value %g is below significance level %g).",
				loadValue,
				ksResult.PValue,
				ksSignificanceLevel,
			),
			Annotations:  nil,
			GeneratorURL: generatorURL,
		}); err != nil {
			slog.Error(
				"failed to queue anomaly alert",
				"service_id", service.Id,
				"indicator_id", indicatorID,
				"timestamp", timestamp,
				"error", err,
			)
		}
	}

	slog.Info(
		"KS test result",
		"anomalous", anomalous,
		"ks_statistic", ksResult.Statistic,
		"p_value", ksResult.PValue,
		"significance_level", ksSignificanceLevel,
		"samples", latencyCount,
		"load", loadValue,
	)

	return anomalous, nil
}

func isStatisticallySignificant(pValue float64) bool {
	return pValue < ksSignificanceLevel
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

func checkedSampleCount(samples []ecdf.Sample) (uint64, error) {
	var total uint64
	for _, sample := range samples {
		var carry uint64
		total, carry = bits.Add64(total, sample.Count, 0)
		if carry != 0 {
			return 0, fmt.Errorf("observation count overflows uint64")
		}
	}
	return total, nil
}
