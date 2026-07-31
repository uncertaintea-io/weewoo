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
	active, err := isActiveGeneration(cfg, service)
	if err != nil {
		return false, err
	}
	if !active {
		return false, nil
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

	jointECDF, baseline, err := readReference(ctx, jointStore, chunks, alerts, service, indicatorID, []time.Time{timestamp})
	if err != nil {
		return false, fmt.Errorf("failed to read current joint ECDF: %w", err)
	}
	if baseline {
		return false, nil
	}

	loadValue := weightedMean(loads)
	cdf, available, err := queryConditionalECDF(ctx, jointECDF, loadValue)
	if err != nil {
		return false, fmt.Errorf("failed to query joint ECDF: %w", err)
	}
	if !available {
		slog.Info(
			"skipping sample analysis because no JECDF points are available",
			"service_id", service.Id,
			"indicator_id", indicatorID,
			"timestamp", timestamp,
		)
		if err := recordBaseline(ctx, chunks, alerts, service, indicatorID, []time.Time{timestamp}); err != nil {
			return false, err
		}
		return false, nil
	}

	ksResult := oneSampleKS(cdf, latencies, latencyCount)
	anomalous := isStatisticallySignificant(ksResult.PValue)
	description := fmt.Sprintf(
		"Current latency distribution differs from the reference at load %f (KS p-value %g; threshold %g).",
		loadValue, ksResult.PValue, ksSignificanceLevel,
	)
	isHistorical := len(historical) > 0 && historical[0]
	if err := recordAnalysisResult(ctx, cfg, chunks, alerts, service, indicatorID, []time.Time{timestamp}, isHistorical, analysisResult{
		indicator: "Load vs. Latency", load: loadValue, pValue: ksResult.PValue, anomalous: anomalous, description: description,
	}); err != nil {
		return false, err
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

func queryConditionalECDF(ctx context.Context, joint []byte, x float64) (func(float64) float64, bool, error) {
	cdf, err := ecdf.Query(ctx, joint, x)
	if err != nil {
		return nil, false, err
	}
	return cdf, cdf != nil, nil
}

func oneSampleKS(cdf func(float64) float64, samples []ecdf.Sample, count uint64) kstests.Result {
	sorted := slices.Clone(samples)
	slices.SortFunc(sorted, func(a, b ecdf.Sample) int {
		return cmp.Compare(a.Value, b.Value)
	})
	values := func(yield func(float64, uint64) bool) {
		for _, sample := range sorted {
			if !yield(sample.Value, sample.Count) {
				return
			}
		}
	}
	return kstests.OneSampleIter(cdf, count, iter.Seq2[float64, uint64](values))
}

type analysisResult struct {
	indicator   string
	load        float64
	pValue      float64
	anomalous   bool
	description string
}

func isActiveGeneration(cfg config.Config, service *config.Service) (bool, error) {
	if service.Generation <= 0 {
		return true, nil
	}
	active, err := cfg.ReadService(service.Id)
	if err != nil {
		return false, fmt.Errorf("read active service generation: %w", err)
	}
	if active.Generation == service.Generation {
		return true, nil
	}
	slog.Info("skipping analysis from a superseded service generation", "service_id", service.Id,
		"collected_generation", service.Generation, "active_generation", active.Generation)
	return false, nil
}

func readReference(ctx context.Context, joints ecdf.JointStore, chunks ecdf.ChunkStore, alerts alerting.AnalysisRecorder, service *config.Service, indicatorID int, timestamps []time.Time) ([]byte, bool, error) {
	joint, err := joints.ReadCurrent(ctx, service.Id, indicatorID)
	if !errors.Is(err, sql.ErrNoRows) {
		return joint, false, err
	}
	if err := recordBaseline(ctx, chunks, alerts, service, indicatorID, timestamps); err != nil {
		return nil, false, err
	}
	return nil, true, nil
}

func recordBaseline(ctx context.Context, chunks ecdf.ChunkStore, alerts alerting.AnalysisRecorder, service *config.Service, indicatorID int, timestamps []time.Time) error {
	for _, timestamp := range timestamps {
		var err error
		if alerts != nil {
			err = alerts.RecordBaseline(ctx, service.Id, indicatorID, timestamp)
		} else if chunks != nil {
			err = chunks.WriteVerdict(ctx, service.Id, indicatorID, service.Generation, timestamp, true, 1)
		} else {
			return fmt.Errorf("cannot record baseline without chunk store or alert recorder")
		}
		if err != nil {
			return fmt.Errorf("%w: %w", errVerdictPersistence, err)
		}
	}
	return nil
}

func recordAnalysisResult(ctx context.Context, cfg config.Config, chunks ecdf.ChunkStore, alerts alerting.AnalysisRecorder, service *config.Service, indicatorID int, timestamps []time.Time, historical bool, result analysisResult) error {
	if len(timestamps) == 0 {
		return fmt.Errorf("analysis has no chunks")
	}
	primary := timestamps[len(timestamps)-1]
	for _, timestamp := range timestamps[:len(timestamps)-1] {
		if chunks == nil {
			return fmt.Errorf("nil chunk store")
		}
		if err := chunks.WriteVerdict(ctx, service.Id, indicatorID, service.Generation, timestamp, !result.anomalous, result.pValue); err != nil {
			return fmt.Errorf("%w: %w", errVerdictPersistence, err)
		}
	}
	if historical || alerts == nil {
		if chunks == nil {
			return fmt.Errorf("nil chunk store")
		}
		if err := chunks.WriteVerdict(ctx, service.Id, indicatorID, service.Generation, primary, !result.anomalous, result.pValue); err != nil {
			return fmt.Errorf("%w: %w", errVerdictPersistence, err)
		}
		return nil
	}
	generatorURL, _ := cfg.GetConfig("alert_generator_url")
	if err := alerts.RecordAnalysis(ctx, alerting.AnalysisOutcome{ServiceID: service.Id, ServiceName: service.Name,
		IndicatorID: indicatorID, Indicator: result.indicator, Timestamp: primary, Load: result.load,
		PValue: result.pValue, Threshold: ksSignificanceLevel, Anomalous: result.anomalous,
		GeneratorURL: generatorURL, Description: result.description, TechnicalDetails: result.description}); err != nil {
		return fmt.Errorf("%w: %w", errVerdictPersistence, err)
	}
	return nil
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
