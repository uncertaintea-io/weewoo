package collection

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
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
func analyzeSample(ctx context.Context, cfg config.Config, jointStore ecdf.JointStore, chunks ecdf.ChunkStore, alerts alerting.AnalysisRecorder, service *config.Service, indicatorID int, timestamp time.Time, loads, latencies []ecdf.Sample, historical ...bool) (bool, error) {
	if cfg == nil {
		return false, fmt.Errorf("nil config")
	}
	if jointStore == nil {
		return false, fmt.Errorf("nil joint ECDF store")
	}
	if service.Generation > 0 {
		active, err := cfg.ReadService(service.Id)
		if err != nil {
			return false, fmt.Errorf("read active service generation: %w", err)
		}
		if active.Generation != service.Generation {
			slog.Info(
				"skipping analysis from a superseded service generation",
				"service_id", service.Id,
				"collected_generation", service.Generation,
				"active_generation", active.Generation,
				"timestamp", timestamp,
			)
			return false, nil
		}
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
		if errors.Is(err, sql.ErrNoRows) && alerts != nil {
			if recordErr := alerts.RecordBaseline(ctx, service.Id, indicatorID, timestamp); recordErr != nil {
				return false, fmt.Errorf("%w: %w", errVerdictPersistence, recordErr)
			}
			return false, nil
		}
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
		if alerts != nil {
			if err := alerts.RecordBaseline(ctx, service.Id, indicatorID, timestamp); err != nil {
				return false, fmt.Errorf("%w: %w", errVerdictPersistence, err)
			}
		}
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
	generatorURL, _ := cfg.GetConfig("alert_generator_url")
	description := fmt.Sprintf(
		"Current latency distribution differs from the reference at load %f (KS p-value %g; threshold %g).",
		loadValue, ksResult.PValue, ksSignificanceLevel,
	)
	isHistorical := len(historical) > 0 && historical[0]
	if isHistorical {
		if chunks == nil {
			return false, fmt.Errorf("nil chunk store")
		}
		if err := chunks.WriteVerdict(ctx, service.Id, indicatorID, service.Generation, timestamp, !anomalous, ksResult.PValue); err != nil {
			return false, fmt.Errorf("%w: %w", errVerdictPersistence, err)
		}
	} else if alerts != nil {
		if err := alerts.RecordAnalysis(ctx, alerting.AnalysisOutcome{
			ServiceID: service.Id, ServiceName: service.Name, IndicatorID: indicatorID,
			Indicator: "Load vs. Latency", Timestamp: timestamp, Load: loadValue,
			PValue: ksResult.PValue, Threshold: ksSignificanceLevel, Anomalous: anomalous,
			GeneratorURL: generatorURL, Description: description, TechnicalDetails: description,
		}); err != nil {
			return false, fmt.Errorf("%w: %w", errVerdictPersistence, err)
		}
	} else {
		if chunks == nil {
			return false, fmt.Errorf("nil chunk store")
		}
		if err := chunks.WriteVerdict(ctx, service.Id, indicatorID, service.Generation, timestamp, !anomalous, ksResult.PValue); err != nil {
			return false, fmt.Errorf("%w: %w", errVerdictPersistence, err)
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
