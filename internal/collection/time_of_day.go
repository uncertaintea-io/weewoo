package collection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/alerting"
	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
	"github.com/uncertaintea-io/weewoo/internal/kstests"
)

const timeOfDayAnalysisWindow = 5 * time.Minute

func analyzeTimeOfDay(ctx context.Context, cfg config.Config, jointStore ecdf.JointStore, chunks ecdf.ChunkStore,
	alerts alerting.AnalysisRecorder, service *config.Service, windowEnd time.Time, observations []LoadObservation,
	verdictTimestamps []time.Time, historical bool) (bool, error) {
	if cfg == nil || jointStore == nil || chunks == nil {
		return false, fmt.Errorf("time-of-day analyzer dependencies must not be nil")
	}
	if len(verdictTimestamps) == 0 {
		return false, fmt.Errorf("time-of-day analysis has no chunks")
	}
	active, err := cfg.ReadService(service.Id)
	if err != nil {
		return false, fmt.Errorf("read active service generation: %w", err)
	}
	if active.Generation != service.Generation {
		return false, nil
	}
	_, ready, err := timeOfDayCoverage(ctx, chunks, service.Id, service.Generation, service.Interval)
	if err != nil {
		return false, fmt.Errorf("measure time-of-day readiness: %w", err)
	}
	if !ready {
		return false, recordTimeOfDayBaseline(ctx, chunks, alerts, service, verdictTimestamps)
	}

	joint, err := jointStore.ReadCurrent(ctx, service.Id, TimeOfDayIndicator)
	if errors.Is(err, sql.ErrNoRows) {
		return false, recordTimeOfDayBaseline(ctx, chunks, alerts, service, verdictTimestamps)
	}
	if err != nil {
		return false, fmt.Errorf("read time-of-day ECDF: %w", err)
	}

	cutoff := windowEnd.Add(-timeOfDayAnalysisWindow)
	percentiles := make([]float64, 0, len(observations))
	var sum float64
	for _, observation := range observations {
		if observation.Timestamp.Before(cutoff) || observation.Timestamp.After(windowEnd) {
			continue
		}
		cdf, queryErr := ecdf.Query(ctx, joint, timeOfDayBucket(observation.Timestamp, service.Interval))
		if queryErr != nil {
			return false, fmt.Errorf("query time-of-day ECDF: %w", queryErr)
		}
		if cdf == nil {
			return false, recordTimeOfDayBaseline(ctx, chunks, alerts, service, verdictTimestamps)
		}
		percentile := cdf(observation.Value)
		percentiles = append(percentiles, percentile)
		sum += percentile
	}
	if len(percentiles) == 0 {
		return false, fmt.Errorf("time-of-day analysis window has no observations")
	}
	slices.Sort(percentiles)
	result := kstests.OneSample(func(value float64) float64 {
		if value < 0 {
			return 0
		}
		if value > 1 {
			return 1
		}
		return value
	}, percentiles)
	anomalous := isStatisticallySignificant(result.PValue)
	primary := verdictTimestamps[len(verdictTimestamps)-1]
	for _, timestamp := range verdictTimestamps[:len(verdictTimestamps)-1] {
		if err := chunks.WriteVerdict(ctx, service.Id, TimeOfDayIndicator, service.Generation, timestamp, !anomalous, result.PValue); err != nil {
			return false, fmt.Errorf("%w: %w", errVerdictPersistence, err)
		}
	}
	if historical || alerts == nil {
		if err := chunks.WriteVerdict(ctx, service.Id, TimeOfDayIndicator, service.Generation, primary, !anomalous, result.PValue); err != nil {
			return false, fmt.Errorf("%w: %w", errVerdictPersistence, err)
		}
		return anomalous, nil
	}
	direction := "lower"
	if sum/float64(len(percentiles)) >= .5 {
		direction = "higher"
	}
	description := fmt.Sprintf("Current load is %s than its UTC time-of-day reference (KS p-value %g; threshold %g).", direction, result.PValue, ksSignificanceLevel)
	generatorURL, _ := cfg.GetConfig("alert_generator_url")
	if err := alerts.RecordAnalysis(ctx, alerting.AnalysisOutcome{ServiceID: service.Id, ServiceName: service.Name,
		IndicatorID: TimeOfDayIndicator, Indicator: "Load vs. UTC Time of Day", Timestamp: primary,
		Load: weightedObservationMean(observations), PValue: result.PValue, Threshold: ksSignificanceLevel,
		Anomalous: anomalous, GeneratorURL: generatorURL, Description: description, TechnicalDetails: description}); err != nil {
		return false, fmt.Errorf("%w: %w", errVerdictPersistence, err)
	}
	return anomalous, nil
}

func recordTimeOfDayBaseline(ctx context.Context, chunks ecdf.ChunkStore, alerts alerting.AnalysisRecorder, service *config.Service, timestamps []time.Time) error {
	for _, timestamp := range timestamps {
		var err error
		if alerts != nil {
			err = alerts.RecordBaseline(ctx, service.Id, TimeOfDayIndicator, timestamp)
		} else {
			err = chunks.WriteVerdict(ctx, service.Id, TimeOfDayIndicator, service.Generation, timestamp, true, 1)
		}
		if err != nil {
			return fmt.Errorf("%w: %w", errVerdictPersistence, err)
		}
	}
	return nil
}

func weightedObservationMean(observations []LoadObservation) float64 {
	if len(observations) == 0 {
		return 0
	}
	var sum float64
	for _, observation := range observations {
		sum += observation.Value
	}
	return sum / float64(len(observations))
}
