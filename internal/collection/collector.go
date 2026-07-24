package collection

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

const (
	LoadLatencyIndicator = 1
	TimeOfDayIndicator   = 2
)

type Collector interface {
	Stop()
	Schedule(service *config.Service) error
	Import(ctx context.Context, service *config.Service, start, end time.Time) error
}

type collector struct {
	client     *http.Client
	chunkStore ecdf.ChunkStore
	scheduler  *IntervalScheduler
	analyzer   AnalysisQueue
}

// this creates a collector that can be used to collect samples from the prometheus server
func NewCollector(client *http.Client, chunkStore ecdf.ChunkStore, scheduler *IntervalScheduler, analyzer AnalysisQueue) Collector {
	if client == nil {
		client = http.DefaultClient
	}
	c := &collector{
		client:     client,
		chunkStore: chunkStore,
		scheduler:  scheduler,
		analyzer:   analyzer,
	}
	return c
}

func (c *collector) Stop() {
	c.scheduler.Stop()
}

func (c *collector) Schedule(service *config.Service) error {
	return c.scheduler.AddCallback(service.Id, service.Interval, func(ctx context.Context, start time.Time, end time.Time) IntervalResult {
		slog.Info("Collecting sample", "service", service.Name, "start", start, "end", end)
		if err := c.collectSamples(ctx, service, start, end); err != nil {
			slog.Error("Failed to collect samples", "error", err)
			return IntervalRetry(err)
		}
		slog.Info("Collected samples", "service", service.Name, "start", start, "end", end)
		return IntervalSuccess()
	})
}

// Import collects an explicit historical window for a service.
func (c *collector) Import(ctx context.Context, service *config.Service, start, end time.Time) error {
	return c.collectSamples(ctx, service, start, end)
}

// this collects the samples from the prometheus server and writes them to the chunk store
func (c *collector) collectSamples(ctx context.Context, service *config.Service, start, end time.Time) error {
	loadValue, err := QueryPrometheusRange(ctx, c.client, service.PrometheusURL, service.LoadQuery, start, end)
	if err != nil {
		return err
	}
	latencyValue, err := QueryPrometheusRange(ctx, c.client, service.PrometheusURL, service.LatencyQuery, start, end)
	if err != nil {
		return err
	}
	loads := ecdf.CountSamples(loadValue)
	latencies := ecdf.CountSamples(latencyValue)
	chunk, err := ecdf.Encode(end, loads, latencies)
	if err != nil {
		return err
	}
	if err := c.chunkStore.WriteChunk(service.Id, LoadLatencyIndicator, end, chunk); err != nil {
		return err
	}
	if c.analyzer != nil {
		request := AnalysisRequest{
			Service:     *service,
			IndicatorID: LoadLatencyIndicator,
			Timestamp:   end,
			Loads:       loads,
			Latencies:   latencies,
		}
		if err := c.analyzer.Submit(request); err != nil {
			slog.Error(
				"failed to queue sample analysis",
				"service_id", service.Id,
				"indicator_id", LoadLatencyIndicator,
				"timestamp", end,
				"error", err,
			)
		}
	}
	return nil
}
