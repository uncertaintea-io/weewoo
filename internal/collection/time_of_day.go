package collection

import (
	"context"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/alerting"
	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

const timeOfDayAnalysisWindow = 5 * time.Minute

func analyzeTimeOfDay(ctx context.Context, cfg config.Config, jointStore ecdf.JointStore, chunks ecdf.ChunkStore,
	alerts alerting.AnalysisRecorder, service *config.Service, windowEnd time.Time, observations []Observation,
	verdictTimestamps []time.Time, historical bool) (bool, error) {
	// TODO(#138): Rewrite all of this.
	return false, nil

	// if cfg == nil || jointStore == nil || chunks == nil {
	// 	return false, fmt.Errorf("time-of-day analyzer dependencies must not be nil")
	// }
	// if len(verdictTimestamps) == 0 {
	// 	return false, fmt.Errorf("time-of-day analysis has no chunks")
	// }
	// active, err := isActiveGeneration(cfg, service)
	// if err != nil {
	// 	return false, err
	// }
	// if !active {
	// 	return false, nil
	// }
	// readiness, err := ReadModelReadiness(ctx, cfg, chunks, service, TimeOfDayIndicator)
	// if err != nil {
	// 	return false, fmt.Errorf("measure time-of-day readiness: %w", err)
	// }
	// if !readiness.Ready {
	// 	return false, recordBaseline(ctx, chunks, alerts, service, TimeOfDayIndicator, verdictTimestamps)
	// }

	// joint, baseline, err := readReference(ctx, jointStore, chunks, alerts, service, TimeOfDayIndicator, verdictTimestamps)
	// if err != nil {
	// 	return false, fmt.Errorf("read time-of-day ECDF: %w", err)
	// }
	// if baseline {
	// 	return false, nil
	// }

	// cutoff := windowEnd.Add(-timeOfDayAnalysisWindow)
	// percentiles := make([]float64, 0, len(observations))
	// var sum float64
	// for _, observation := range observations {
	// 	if observation.Timestamp.Before(cutoff) || observation.Timestamp.After(windowEnd) {
	// 		continue
	// 	}
	// 	cdf, available, queryErr := queryJointECDF(ctx, joint, timeOfDayBucket(observation.Timestamp, service.Interval))
	// 	if queryErr != nil {
	// 		return false, fmt.Errorf("query time-of-day ECDF: %w", queryErr)
	// 	}
	// 	if !available {
	// 		return false, recordBaseline(ctx, chunks, alerts, service, TimeOfDayIndicator, verdictTimestamps)
	// 	}
	// 	percentile := cdf(observation.Value)
	// 	percentiles = append(percentiles, percentile)
	// 	sum += percentile
	// }
	// if len(percentiles) == 0 {
	// 	return false, fmt.Errorf("time-of-day analysis window has no observations")
	// }
	// // If current loads follow the time-of-day reference, their reference CDF
	// // percentiles follow a uniform distribution on [0, 1]. Use a one-sample KS
	// // test against that uniform CDF to detect a shift across the window without
	// // requiring a second sample of historical observations.
	// percentileSamples := ecdf.CountSamples(percentiles)
	// result := oneSampleKS(func(value float64) float64 {
	// 	if value < 0 {
	// 		return 0
	// 	}
	// 	if value > 1 {
	// 		return 1
	// 	}
	// 	return value
	// }, percentileSamples, uint64(len(percentiles)))
	// anomalous := isStatisticallySignificant(result.PValue)
	// direction := "lower"
	// if sum/float64(len(percentiles)) >= .5 {
	// 	direction = "higher"
	// }
	// description := fmt.Sprintf("Current load is %s than its UTC time-of-day reference (KS p-value %g; threshold %g).", direction, result.PValue, ksSignificanceLevel)
	// if err := recordAnalysisResult(ctx, cfg, chunks, alerts, service, TimeOfDayIndicator, verdictTimestamps, historical, analysisResult{
	// 	indicator: "Load vs. UTC Time of Day", load: weightedObservationMean(observations), pValue: result.PValue,
	// 	anomalous: anomalous, description: description,
	// }); err != nil {
	// 	return false, err
	// }
	// return anomalous, nil
}
