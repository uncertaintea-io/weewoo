package collection

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	serviceInterval = time.Hour

	ReservedCallbackIds = 1000

	ECDFScheduledBuildTimeoutConfigKey = "ecdf_scheduled_build_timeout"
	ECDFBaselineChunksConfigKey        = "ecdf_baseline_chunks"
	defaultECDFScheduledBuildTimeout   = 5 * time.Minute
	defaultECDFBaselineChunks          = 10
)

type CallbackType int

const (
	CollectCallback CallbackType = iota
	BuilderCallback

	MaxCallbackType
)

// CallbackID returns the scheduler ID for one kind of service callback.
func CallbackID(serviceID int, callbackType CallbackType) int {
	return ReservedCallbackIds + serviceID*int(MaxCallbackType) + int(callbackType)
}

// Gets the ID if the service from the config object
func getTargets(cfg config.Config) ([]ecdfBuilderTarget, error) {
	services, err := cfg.ReadAllServices()
	if err != nil {
		return nil, err
	}
	targets := make([]ecdfBuilderTarget, 0, len(services))
	for _, service := range services {
		if service.Paused {
			continue
		}
		targets = append(targets, ecdfBuilderTarget{
			ServiceID:  service.Id,
			CallbackID: CallbackID(service.Id, CollectCallback),
		})
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
	targets, err := getTargets(cfg)
	if err != nil {
		return fmt.Errorf("failed to get service IDs: %w", err)
	}
	slog.Info("ECDF publisher startup configuration")
	for _, target := range targets {
		if err := ScheduleECDFBuilder(target.ServiceID, chunkStore, jointStore, cfg, scheduler); err != nil {
			return err
		}
	}
	return nil
}

// ScheduleECDFBuilder registers the hourly publisher for one service. It is
// used at startup and whenever a service is added while the server is running.
func ScheduleECDFBuilder(serviceID int, chunkStore ecdf.ChunkStore, jointStore ecdf.JointStore, cfg config.Config, scheduler *IntervalScheduler) error {
	if serviceID <= 0 {
		return fmt.Errorf("service ID must be greater than zero")
	}
	if scheduler == nil || jointStore == nil || cfg == nil {
		return fmt.Errorf("ECDF builder dependencies must not be nil")
	}
	buildTimeout, err := configuredDuration(cfg, ECDFScheduledBuildTimeoutConfigKey, defaultECDFScheduledBuildTimeout)
	if err != nil {
		return fmt.Errorf("failed to get build timeout: %w", err)
	}
	callbackID := CallbackID(serviceID, BuilderCallback)
	err = scheduler.AddCallback(callbackID, serviceInterval, func(ctx context.Context, start time.Time, end time.Time) IntervalResult {
		buildCtx, cancel := context.WithTimeout(ctx, buildTimeout)
		defer cancel()

		minimumChunks, err := configuredPositiveInt(cfg, ECDFBaselineChunksConfigKey, defaultECDFBaselineChunks)
		if err != nil {
			return IntervalPermanent(err)
		}
		eligibleChunks, err := chunkStore.CountEligibleChunks(buildCtx, serviceID, LoadLatencyIndicator)
		if err != nil {
			return IntervalRetry(err)
		}
		if eligibleChunks < minimumChunks {
			slog.Info("deferring first joint ECDF build until baseline is ready",
				"service_id", serviceID, "eligible_chunks", eligibleChunks, "required_chunks", minimumChunks)
			return IntervalSuccess()
		}

		bytesWritten, published, err := jointStore.Publish(buildCtx, serviceID, LoadLatencyIndicator, end, func(out io.Writer) error {
			if err := ecdf.BuildJointECDFContext(buildCtx, chunkStore, serviceID, LoadLatencyIndicator, out); err != nil {
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
			slog.Error("failed to publish joint ECDF", "service_id", serviceID, "indicator_id", LoadLatencyIndicator, "stage", stage, "error", err)
			return IntervalRetry(err)
		}
		if !published {
			slog.Info("this is being handled by another publisher", "service_id", serviceID, "indicator_id", LoadLatencyIndicator)
			return IntervalSuccess()
		}
		slog.Info("built joint ECDF", "service_id", serviceID, "indicator_id", LoadLatencyIndicator, "start", start, "end", end, "bytes", bytesWritten)
		return IntervalSuccess()
	}, WithLastEnd(time.Now().UTC().Truncate(serviceInterval).Add(-serviceInterval)))
	if err != nil {
		return fmt.Errorf("failed to add callback for service %d: %w", serviceID, err)
	}
	return nil
}

func configuredPositiveInt(cfg config.Config, key string, fallback int) (int, error) {
	value, err := cfg.GetConfig(key)
	if err != nil {
		return 0, fmt.Errorf("failed to read %s: %w", key, err)
	}
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("invalid %s %q: must be a positive integer", key, value)
	}
	return parsed, nil
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
