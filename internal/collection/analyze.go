package collection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/bits"
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
	cdf, available, err := queryJointECDF(ctx, jointECDF, loadValue)
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

	ksResult := kstests.OneSample(cdf, latencies)
	latencyBucketCount := nonEmptySampleCount(latencies)
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
		"buckets", latencyBucketCount,
		"observations", latencyCount,
		"load", loadValue,
	)

	return anomalous, nil
}

func queryJointECDF(ctx context.Context, joint []byte, x float64) (func(float64) float64, bool, error) {
	xs, ps, err := ecdf.Query(ctx, joint, x)
	if err != nil {
		return nil, false, err
	}
	if len(xs) == 0 {
		return nil, false, nil
	}
	cdf, err := ecdf.LinearInterpolation(xs, ps)
	if err != nil {
		return nil, false, err
	}
	return cdf, true, nil
}

func nonEmptySampleCount(samples []ecdf.Sample) uint64 {
	var count uint64
	for _, sample := range samples {
		if sample.Count > 0 {
			count++
		}
	}
	return count
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

// readReference loads the currently published joint ECDF for an indicator. The
// reference is the comparison model built from eligible chunks from earlier
// collection windows. If no reference has been published yet, readReference
// records the current chunks as baseline data and returns baseline=true so the
// caller can skip anomaly analysis for this window.
func readReference(ctx context.Context, joints ecdf.JointStore, chunks ecdf.ChunkStore, alerts alerting.AnalysisRecorder, service *config.Service, indicatorID int, timestamps []time.Time) ([]byte, bool, error) {
	joint, _, err := joints.ReadCurrent(ctx, service.Id, indicatorID)
	if !errors.Is(err, sql.ErrNoRows) {
		return joint, false, err
	}
	if err := recordBaseline(ctx, chunks, alerts, service, indicatorID, timestamps); err != nil {
		return nil, false, err
	}
	return nil, true, nil
}

// recordBaseline marks chunks as eligible input for the first reference model
// without claiming that they passed anomaly analysis. It is intended for data
// collected while no usable reference exists, when comparison against prior
// behavior is not yet possible. Once a reference is available, callers should
// persist a Good or Bad verdict with recordAnalysisResult instead.
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

// recordAnalysisResult applies one comparison result to the chunks named by
// timestamps. Callers must order timestamps from oldest to newest; the final
// timestamp is the primary chunk representing the scheduler window. Every
// chunk receives the same Good or Bad verdict for future model eligibility,
// but only a live primary chunk is allowed to advance the alert lifecycle.
func recordAnalysisResult(ctx context.Context, cfg config.Config, chunks ecdf.ChunkStore, alerts alerting.AnalysisRecorder, service *config.Service, indicatorID int, timestamps []time.Time, historical bool, result analysisResult) error {
	if len(timestamps) == 0 {
		return fmt.Errorf("analysis has no chunks")
	}

	// A time-of-day window may contain several chunks, but it represents one
	// analysis event. Persist verdicts for all earlier chunks directly so they
	// affect eligibility without creating additional alert occurrences.
	primary := timestamps[len(timestamps)-1]
	for _, timestamp := range timestamps[:len(timestamps)-1] {
		if chunks == nil {
			return fmt.Errorf("nil chunk store")
		}
		if err := chunks.WriteVerdict(ctx, service.Id, indicatorID, service.Generation, timestamp, !result.anomalous, result.pValue); err != nil {
			return fmt.Errorf("%w: %w", errVerdictPersistence, err)
		}
	}

	// Historical analysis must not change live alert state. A nil alert recorder
	// uses the same direct-persistence path for deployments without alerting.
	if historical || alerts == nil {
		if chunks == nil {
			return fmt.Errorf("nil chunk store")
		}
		if err := chunks.WriteVerdict(ctx, service.Id, indicatorID, service.Generation, primary, !result.anomalous, result.pValue); err != nil {
			return fmt.Errorf("%w: %w", errVerdictPersistence, err)
		}
		return nil
	}

	// Delegate the live primary result to the alert recorder, which persists its
	// verdict and updates the service's alert condition as one operation.
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
