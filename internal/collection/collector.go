package collection

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/alerting"
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
	Unschedule(serviceID int)
	Import(ctx context.Context, service *config.Service, start, end time.Time) error
}

type collector struct {
	client     *http.Client
	chunkStore ecdf.ChunkStore
	scheduler  *IntervalScheduler
	analyzer   AnalysisQueue
	recovery   *RecoveryQueue
	events     CollectorEventHandler
}

type CollectorEvent struct {
	ServiceID int
	Kind      string
	Message   string
	At        time.Time
}

type CollectorEventHandler func(CollectorEvent)

type CollectorOption func(*collector)

func WithRecoveryQueue(db *sql.DB, cfg config.Config, recorder alerting.Recorder) CollectorOption {
	return func(c *collector) {
		if db != nil && recorder != nil {
			c.recovery = NewRecoveryQueue(db, cfg, recorder)
		}
	}
}

func WithCollectorEventHandler(handler CollectorEventHandler) CollectorOption {
	return func(c *collector) { c.events = handler }
}

// this creates a collector that can be used to collect samples from the prometheus server
func NewCollector(client *http.Client, chunkStore ecdf.ChunkStore, scheduler *IntervalScheduler, analyzer AnalysisQueue, options ...CollectorOption) Collector {
	if client == nil {
		client = http.DefaultClient
	}
	c := &collector{
		client:     client,
		chunkStore: chunkStore,
		scheduler:  scheduler,
		analyzer:   analyzer,
	}
	for _, option := range options {
		option(c)
	}
	return c
}

func (c *collector) Stop() {
	if c.recovery != nil {
		c.recovery.Stop()
	}
	c.scheduler.Stop()
}

func (c *collector) Unschedule(serviceID int) {
	c.scheduler.RemoveCallback(CallbackID(serviceID, CollectCallback))
	if c.recovery != nil {
		c.recovery.Unregister(serviceID)
	}
}

func (c *collector) Schedule(service *config.Service) error {
	if c.recovery != nil {
		c.recovery.Register(service, c.collectHistorical)
	}
	c.emit(service.Id, "tracking_started", "Prometheus collection is scheduled")
	callbackID := CallbackID(service.Id, CollectCallback)
	return c.scheduler.AddCallback(callbackID, service.Interval, func(ctx context.Context, start time.Time, end time.Time) IntervalResult {
		slog.Info("Collecting sample", "service", service.Name, "start", start, "end", end)
		if c.recovery != nil {
			pending, err := c.recovery.HasPending(ctx, service.Id)
			if err != nil {
				return IntervalRetry(err)
			}
			if pending {
				if err := c.recovery.EnqueueDeferred(ctx, service, start, end); err != nil {
					return IntervalRetry(err)
				}
				c.emit(service.Id, "collection_delayed", "Collection is catching up chronologically")
				return IntervalSuccess()
			}
		}
		if err := c.collectSamples(ctx, service, start, end, false); err != nil {
			slog.Error("Failed to collect samples", "error", err)
			if c.recovery != nil {
				if queueErr := c.recovery.EnqueueFailure(ctx, service, start, end, err); queueErr != nil {
					return IntervalRetry(errors.Join(err, queueErr))
				}
				c.emit(service.Id, "collection_failed", err.Error())
				return IntervalSuccess()
			}
			return IntervalRetry(err)
		}
		c.emit(service.Id, "collection_succeeded", "Prometheus metrics collected successfully")
		slog.Info("Collected samples", "service", service.Name, "start", start, "end", end)
		return IntervalSuccess()
	})
}

// Import collects an explicit historical window for a service.
func (c *collector) Import(ctx context.Context, service *config.Service, start, end time.Time) error {
	return c.collectSamples(ctx, service, start, end, true)
}

// this collects the samples from the prometheus server and writes them to the chunk store
func (c *collector) collectHistorical(ctx context.Context, service *config.Service, start, end time.Time) error {
	err := c.collectSamples(ctx, service, start, end, true)
	if err == nil {
		c.emit(service.Id, "collection_backlog_recovered", "Recovered a historical collection window")
	}
	return err
}

func (c *collector) collectSamples(ctx context.Context, service *config.Service, start, end time.Time, historicalOption ...bool) error {
	historical := len(historicalOption) > 0 && historicalOption[0]
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
			Historical:  historical,
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

func (c *collector) emit(serviceID int, kind, message string) {
	if c.events != nil {
		c.events(CollectorEvent{ServiceID: serviceID, Kind: kind, Message: message, At: time.Now().UTC()})
	}
}
