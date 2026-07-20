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
	Schedule(service *config.Service)
	Import(ctx context.Context, service *config.Service, start, end time.Time) error
}

type collector struct {
	client    *http.Client
	store     ecdf.ChunkStore
	scheduler *IntervalScheduler
}

// this creates a collector that can be used to collect samples from the prometheus server
func NewCollector(client *http.Client, store ecdf.ChunkStore, scheduler *IntervalScheduler) Collector {
	if client == nil {
		client = http.DefaultClient
	}
	c := &collector{
		client:    client,
		store:     store,
		scheduler: scheduler,
	}
	return c
}

func (c *collector) Stop() {
	c.scheduler.Stop()
}

func (c *collector) Schedule(service *config.Service) {
	c.scheduler.AddCallback(service.Id, service.Interval, func(ctx context.Context, start time.Time, end time.Time) IntervalResult {
		slog.Info("Collecting sample", "service", service.Name, "start", start, "end", end)
		if err := c.collectSamples(ctx, service, start, end); err != nil {
			return IntervalRetry(err)
		}
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
	chunk, err := ecdf.Encode(end, ecdf.CountSamples(loadValue), ecdf.CountSamples(latencyValue))
	if err != nil {
		return err
	}
	return c.store.WriteChunk(service.Id, LoadLatencyIndicator, end, chunk)
}
