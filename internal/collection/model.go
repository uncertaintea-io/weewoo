// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

package collection

import (
	"context"
	"fmt"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

const (
	timeOfDayTrainingDays     = 5
	timeOfDayTrainingRange    = timeOfDayTrainingDays * 24 * time.Hour
	timeOfDayRequiredCoverage = 0.95
)

var indicatorIDs = []int{ecdf.LoadLatencyIndicator, ecdf.TimeOfDayIndicator}

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
	case ecdf.LoadLatencyIndicator:
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
	case ecdf.TimeOfDayIndicator:
		return readTimeOfDayReadiness(ctx, store, service, indicatorID)
	default:
		return ModelReadiness{}, fmt.Errorf("unknown indicator %d", indicatorID)
	}
}

func readTimeOfDayReadiness(ctx context.Context, store ecdf.ChunkStore, service *config.Service, indicatorID int) (ModelReadiness, error) {
	if service.Interval <= 0 {
		return ModelReadiness{}, fmt.Errorf("invalid service interval")
	}
	expected := int(timeOfDayTrainingRange / service.Interval)
	if timeOfDayTrainingRange%service.Interval != 0 {
		expected++
	}
	eligible, err := store.CountEligibleChunks(ctx, service.Id, indicatorID, service.Generation)
	if err != nil {
		return ModelReadiness{}, err
	}
	coverage := min(float64(eligible)/float64(expected), 1)
	return ModelReadiness{
		Ready:    coverage >= timeOfDayRequiredCoverage,
		Coverage: coverage,
		Progress: coverage,
		Required: timeOfDayTrainingDays,
		Eligible: eligible,
	}, nil
}
