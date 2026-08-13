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

var errServiceGenerationChanged = errors.New("service generation changed during ECDF publication")

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
	callbackID := CallbackID(serviceID, BuilderCallback)
	err := scheduler.AddCallback(callbackID, serviceInterval, func(ctx context.Context, _ time.Time, end time.Time) IntervalResult {
		if err := BuildServiceECDFs(ctx, chunkStore, jointStore, cfg, serviceID, end); err != nil {
			return IntervalRetry(err)
		}
		return IntervalSuccess()
	}, WithLastEnd(time.Now().UTC().Truncate(serviceInterval).Add(-serviceInterval)))
	if err != nil {
		return fmt.Errorf("failed to add callback for service %d: %w", serviceID, err)
	}
	return nil
}

// BuildServiceECDFs publishes every ready model for one service. Scheduled
// publication and post-import publication share this interface.
func BuildServiceECDFs(ctx context.Context, chunkStore ecdf.ChunkStore, jointStore ecdf.JointStore, cfg config.Config, serviceID int, intervalEnd time.Time) error {
	if serviceID <= 0 {
		return fmt.Errorf("service ID must be greater than zero")
	}
	if chunkStore == nil || jointStore == nil || cfg == nil {
		return fmt.Errorf("ECDF builder dependencies must not be nil")
	}
	if intervalEnd.IsZero() {
		return fmt.Errorf("ECDF build interval end must not be zero")
	}
	buildTimeout, err := configuredDuration(cfg, ECDFScheduledBuildTimeoutConfigKey, defaultECDFScheduledBuildTimeout)
	if err != nil {
		return fmt.Errorf("failed to get build timeout: %w", err)
	}
	buildCtx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()

	service, err := cfg.ReadService(serviceID)
	if err != nil {
		return fmt.Errorf("read service generation: %w", err)
	}
	for _, indicatorID := range indicatorIDs {
		readiness, err := ReadModelReadiness(buildCtx, cfg, chunkStore, service, indicatorID)
		if err != nil {
			return fmt.Errorf("measure indicator %d readiness: %w", indicatorID, err)
		}
		if !readiness.Ready {
			slog.Info("deferring joint ECDF build until reference data is ready", "service_id", serviceID,
				"indicator_id", indicatorID, "coverage", readiness.Coverage, "eligible", readiness.Eligible, "required", readiness.Required)
			continue
		}
		result := publishIndicator(buildCtx, cfg, chunkStore, jointStore, service, indicatorID, intervalEnd.Add(-serviceInterval), intervalEnd)
		if result.Err != nil {
			return result.Err
		}
	}
	return nil
}

func publishIndicator(ctx context.Context, cfg config.Config, chunks ecdf.ChunkStore, joints ecdf.JointStore, service *config.Service, indicatorID int, start, end time.Time) IntervalResult {
	bytesWritten, published, err := joints.Publish(ctx, service.Id, indicatorID, end, func(out io.Writer) error {
		active, err := cfg.ReadService(service.Id)
		if err != nil {
			return fmt.Errorf("re-read service generation under publication lock: %w", err)
		}
		if active.Generation != service.Generation {
			return fmt.Errorf("%w: started generation %d, active generation %d", errServiceGenerationChanged, service.Generation, active.Generation)
		}
		if err := ecdf.BuildJointECDF(ctx, chunks, service.Id, indicatorID, service.Generation, out); err != nil {
			return fmt.Errorf("ECDF generation failed: %w", err)
		}
		return nil
	})
	if errors.Is(err, errServiceGenerationChanged) {
		slog.Info("discarding superseded joint ECDF publication", "service_id", service.Id, "indicator_id", indicatorID, "generation", service.Generation)
		return IntervalSuccess()
	}
	if err != nil {
		stage := "publication"
		if ctx.Err() != nil {
			stage = "scheduled build deadline"
			err = fmt.Errorf("%s failed: %w", stage, errors.Join(err, ctx.Err()))
		}
		slog.Error("failed to publish joint ECDF", "service_id", service.Id, "indicator_id", indicatorID, "stage", stage, "error", err)
		return IntervalRetry(err)
	}
	if !published {
		slog.Info("this is being handled by another publisher", "service_id", service.Id, "indicator_id", indicatorID)
		return IntervalSuccess()
	}
	slog.Info("built joint ECDF", "service_id", service.Id, "indicator_id", indicatorID, "start", start, "end", end, "bytes", bytesWritten)
	return IntervalSuccess()
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
