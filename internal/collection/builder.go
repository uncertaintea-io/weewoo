package collection

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

type ecdfBuilderTarget struct {
	ServiceID  int
	CallbackID int
}

const (
	serviceInterval             = time.Hour
	ecdfBuilderCallbackIDOffset = 1000

	ECDFScheduledBuildTimeoutConfigKey = "ecdf_scheduled_build_timeout"
	ECDFPublisherEnabledEnv            = "ECDF_PUBLISHER_ENABLED"
	defaultECDFScheduledBuildTimeout   = 5 * time.Minute
)

// Gets the ID if the service from the config object
func getTargets(cfg config.Config) ([]ecdfBuilderTarget, error) {
	services, err := cfg.ReadAllServices()
	if err != nil {
		return nil, err
	}
	targets := make([]ecdfBuilderTarget, len(services))
	for i, service := range services {
		targets[i] = ecdfBuilderTarget{
			ServiceID:  service.Id,
			CallbackID: ecdfBuilderCallbackIDOffset + service.Id,
		}
	}
	return targets, nil
}

// StartECDFBuilder schedules one hourly builder callback. The scheduler owns the
// timer loop and runs callbacks in goroutines when their window is due.
func StartECDFBuilder(chunkStore ecdf.ChunkStore, jointStore ecdf.JointStore, cfg config.Config, scheduler *IntervalScheduler) error {
	if scheduler == nil {
		return fmt.Errorf("nil interval scheduler")
	}
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	if jointStore == nil {
		return fmt.Errorf("nil joint ECDF store")
	}
	publisherEnabled, err := ecdfPublisherEnabled()
	if err != nil {
		return err
	}
	slog.Info("ECDF publisher startup configuration", "enabled", publisherEnabled, "coordination_mode", "postgres_advisory_lock")
	if !publisherEnabled {
		return nil
	}
	buildTimeout, err := configuredDuration(cfg, ECDFScheduledBuildTimeoutConfigKey, defaultECDFScheduledBuildTimeout)
	if err != nil {
		return err
	}
	targets, err := getTargets(cfg)
	if err != nil {
		return fmt.Errorf("failed to get service IDs: %w", err)
	}
	lastCompletedBoundary := time.Now().UTC().Truncate(serviceInterval)
	for _, target := range targets {
		err = scheduler.AddCallback(target.CallbackID, serviceInterval, func(ctx context.Context, start time.Time, end time.Time) IntervalResult {
			buildCtx, cancel := context.WithTimeout(ctx, buildTimeout)
			defer cancel()

			bytesWritten, published, err := jointStore.Publish(buildCtx, target.ServiceID, LoadLatencyIndicator, end, func(out io.Writer) error {
				if err := ecdf.BuildJointECDFContext(buildCtx, chunkStore, target.ServiceID, LoadLatencyIndicator, out); err != nil {
					return fmt.Errorf("ECDF generation failed: %w", err)
				}
				return nil
			})
			if err != nil {
				stage := "publication"
				if buildCtx.Err() != nil {
					stage = "scheduled build deadline"
					err = fmt.Errorf("%s failed: %w", stage, errors.Join(err, buildCtx.Err()))
				}
				slog.Error("failed to publish joint ECDF", "service_id", target.ServiceID, "indicator_id", LoadLatencyIndicator, "stage", stage, "error", err)
				return IntervalRetry(err)
			}
			if !published {
				slog.Info("this is being handled by another publisher", "service_id", target.ServiceID, "indicator_id", LoadLatencyIndicator)
				return IntervalSuccess()
			}
			slog.Info("built joint ECDF", "service_id", target.ServiceID, "indicator_id", LoadLatencyIndicator, "start", start, "end", end, "bytes", bytesWritten)
			return IntervalSuccess()
		}, WithLastEnd(lastCompletedBoundary.Add(-serviceInterval)))
		if err != nil {
			return fmt.Errorf("failed to add callback for service %d: %w", target.ServiceID, err)
		}
	}
	return nil
}

func ecdfPublisherEnabled() (bool, error) {
	value, ok := os.LookupEnv(ECDFPublisherEnabledEnv)
	if !ok || value == "" {
		return true, nil
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("invalid %s %q: %w", ECDFPublisherEnabledEnv, value, err)
	}
	return enabled, nil
}

func configuredDuration(cfg config.Config, key string, fallback time.Duration) (time.Duration, error) {
	value, err := cfg.GetConfig(key)
	if err != nil {
		return 0, fmt.Errorf("failed to read %s: %w", key, err)
	}
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("invalid %s %q: must be a positive duration", key, value)
	}
	return duration, nil
}
