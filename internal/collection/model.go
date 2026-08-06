package collection

import (
	"context"
	"fmt"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

const (
	timeOfDayBaselineDays     = 5
	timeOfDayRequiredCoverage = 0.95
)

var indicatorIDs = []int{LoadLatencyIndicator, TimeOfDayIndicator}

// ModelReadiness describes whether an indicator has enough eligible reference
// data to publish and analyze. Coverage is normalized to [0, 1].
type ModelReadiness struct {
	Ready    bool
	Coverage float64
	Progress float64
	Required int
	Eligible int
}

// ReadModelReadiness applies the readiness policy for an indicator. Publication,
// analysis, and status reporting all use this function so they cannot disagree.
func ReadModelReadiness(ctx context.Context, cfg config.Config, store ecdf.ChunkStore, service *config.Service, indicatorID int) (ModelReadiness, error) {
	if cfg == nil || store == nil || service == nil {
		return ModelReadiness{}, fmt.Errorf("model readiness dependencies must not be nil")
	}
	switch indicatorID {
	case LoadLatencyIndicator:
		required, err := configuredPositiveInt(cfg, ECDFBaselineChunksConfigKey, defaultECDFBaselineChunks)
		if err != nil {
			return ModelReadiness{}, err
		}
		eligible, err := store.CountEligibleChunks(ctx, service.Id, indicatorID, service.Generation)
		if err != nil {
			return ModelReadiness{}, err
		}
		coverage := min(float64(eligible)/float64(required), 1)
		return ModelReadiness{Ready: eligible >= required, Coverage: coverage, Progress: coverage, Required: required, Eligible: eligible}, nil
	case TimeOfDayIndicator:
		return readBucketReadiness(ctx, store, service, indicatorID, timeOfDayBaselineDays, timeOfDayRequiredCoverage)
	default:
		return ModelReadiness{}, fmt.Errorf("unknown indicator %d", indicatorID)
	}
}

func readBucketReadiness(ctx context.Context, store ecdf.ChunkStore, service *config.Service, indicatorID, requiredDates int, requiredCoverage float64) (ModelReadiness, error) {
	if service.Interval <= 0 {
		return ModelReadiness{}, fmt.Errorf("invalid service interval")
	}
	totalBuckets := int((24*time.Hour + service.Interval - 1) / service.Interval)
	datesByBucket := make(map[int]map[string]struct{})
	chunks := make(chan []byte, 2)
	errCh := make(chan error, 1)
	go func() {
		defer close(chunks)
		errCh <- store.ScanGoodChunks(ctx, service.Id, indicatorID, service.Generation, chunks)
	}()
	var decodeErr error
	for chunk := range chunks {
		if decodeErr != nil {
			continue
		}
		timestamp, xs, _, err := ecdf.Decode(chunk)
		if err != nil {
			decodeErr = fmt.Errorf("decode indicator %d chunk: %w", indicatorID, err)
			continue
		}
		if len(xs) != 1 || xs[0].Count != 1 {
			decodeErr = fmt.Errorf("indicator %d bucket chunk must contain one x observation", indicatorID)
			continue
		}
		bucket := int(xs[0].Value)
		if bucket < 0 || bucket >= totalBuckets {
			decodeErr = fmt.Errorf("indicator %d bucket %d outside range", indicatorID, bucket)
			continue
		}
		if datesByBucket[bucket] == nil {
			datesByBucket[bucket] = make(map[string]struct{})
		}
		datesByBucket[bucket][timestamp.UTC().Format(time.DateOnly)] = struct{}{}
	}
	if err := <-errCh; err != nil {
		return ModelReadiness{}, err
	}
	if decodeErr != nil {
		return ModelReadiness{}, decodeErr
	}
	qualified := 0
	progressUnits := 0
	for _, dates := range datesByBucket {
		if len(dates) >= requiredDates {
			qualified++
		}
		progressUnits += min(len(dates), requiredDates)
	}
	coverage := float64(qualified) / float64(totalBuckets)
	progress := float64(progressUnits) / float64(totalBuckets*requiredDates)
	return ModelReadiness{Ready: coverage >= requiredCoverage, Coverage: coverage, Progress: progress, Required: requiredDates, Eligible: qualified}, nil
}
