package collection

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

type ecdfBuilderTarget struct {
	ServiceID int
	CallbackID int
}

const (
	serviceInterval             = time.Hour
	ecdfBuilderCallbackIDOffset = 1000
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

// TODO make sure the generated ecdf is stored somewhere
// StartECDFBuilder schedules one hourly builder callback. The scheduler owns the
// timer loop and runs callbacks in goroutines when their window is due.
func StartECDFBuilder(chunkStore ecdf.ChunkStore, cfg config.Config, scheduler *IntervalScheduler) error {
	if scheduler == nil {
		return fmt.Errorf("nil interval scheduler")
	}
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	targets, err := getTargets(cfg)
	if err != nil {
		return fmt.Errorf("failed to get service IDs: %w", err)
	}
	for _, target := range targets {
		err = scheduler.AddCallback(target.CallbackID, serviceInterval, func(ctx context.Context, start time.Time, end time.Time) IntervalResult {
			out, err := ecdf.BuildJointECDFContext(ctx, chunkStore, target.ServiceID, LoadLatencyIndicator, start, end)
			if err != nil {
				slog.Error("failed to build joint ECDF", "service_id", target.ServiceID, "indicator_id", LoadLatencyIndicator, "start", start, "end", end, "error", err)
				return IntervalRetry(err)
			}
			slog.Info("built joint ECDF", "service_id", target.ServiceID, "indicator_id", LoadLatencyIndicator, "start", start, "end", end, "bytes", len(out))
			return IntervalSuccess()
		}, WithLastEnd(time.Now().Add(-serviceInterval)))
		if err != nil {
			return fmt.Errorf("failed to add callback for service %d: %w", target.ServiceID, err)
		}
	}
	return nil
}
