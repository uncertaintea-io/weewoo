package collection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	Import(ctx context.Context, service *config.Service, start, end time.Time, progress ImportProgressHandler) (ImportSummary, error)
}

type ImportSummary struct {
	TotalWindows    int
	ImportedWindows int
	// GapWindows is the number of historical collection intervals for which
	// Prometheus returned no usable metrics. These monitoring gaps do not fail
	// the import and do not contribute time chunks to the reference ECDF.
	GapWindows int
}

type historicalImportWindowPolicy struct{}

func (historicalImportWindowPolicy) Classify(_ windowAttempt, err error) windowResult {
	switch {
	case err == nil:
		return windowResult{Outcome: windowCompleted}
	case errors.Is(err, errWindowHasNoMetrics), errors.Is(err, ErrNoPrometheusData):
		return windowResult{Outcome: windowMonitoringGap, Err: err}
	default:
		return windowResult{Outcome: windowFailed, Err: err}
	}
}

type ImportProgress struct {
	Percent int
	ImportSummary
}

type ImportProgressHandler func(ImportProgress)

type collectionRecovery interface {
	Stop()
	Register(service *config.Service, collect historicalCollector)
	Unregister(serviceID int)
	EnqueueFailure(ctx context.Context, service *config.Service, start, end time.Time, failure error) error
	ResolveCollection(ctx context.Context, serviceID int, at time.Time) error
}

type collector struct {
	client     *http.Client
	chunkStore ecdf.ChunkStore
	scheduler  *IntervalScheduler
	analyzer   AnalysisQueue
	recovery   collectionRecovery
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
		slog.Info("Collecting sample", "service_id", service.Id, "service", service.Name, "start", start, "end", end)
		if err := c.collectSamples(ctx, service, start, end, false); err != nil {
			slog.Error("Failed to collect samples", "service_id", service.Id, "service", service.Name, "start", start, "end", end, "error", err)
			if c.recovery != nil {
				if queueErr := c.recovery.EnqueueFailure(ctx, service, start, end, err); queueErr != nil {
					return IntervalRetry(errors.Join(err, queueErr))
				}
				c.emit(service.Id, "collection_failed", err.Error())
				return IntervalSuccess()
			}
			return IntervalRetry(err)
		}
		if c.recovery != nil {
			if err := c.recovery.ResolveCollection(ctx, service.Id, time.Now().UTC()); err != nil {
				slog.Error(
					"failed to resolve collection alert after successful live collection",
					"service_id", service.Id,
					"service", service.Name,
					"error", err,
				)
			}
		}
		c.emit(service.Id, "collection_succeeded", "Prometheus metrics collected successfully")
		slog.Info("Collected samples", "service_id", service.Id, "service", service.Name, "start", start, "end", end)
		return IntervalSuccess()
	})
}

// Import collects an explicit historical range for a service, one Time chunk at a time.
func (c *collector) Import(ctx context.Context, service *config.Service, start, end time.Time, progress ImportProgressHandler) (ImportSummary, error) {
	summary := ImportSummary{TotalWindows: countHistoricalWindows(start, end, service.Interval)}
	if service.Interval <= 0 {
		return summary, fmt.Errorf("invalid service interval")
	}
	processor := newWindowProcessor(time.Now)
	for windowStart := start; windowStart.Before(end); {
		windowEnd := windowStart.Add(service.Interval)
		if windowEnd.After(end) {
			windowEnd = end
		}
		window := collectionWindow{Start: windowStart, End: windowEnd}
		result := processor.Process(ctx, windowAttempt{Window: window}, func(ctx context.Context) error {
			return c.collectSamples(ctx, service, window.Start, window.End, true)
		}, historicalImportWindowPolicy{})
		switch result.Outcome {
		case windowCompleted:
			summary.ImportedWindows++
		case windowMonitoringGap:
			summary.GapWindows++
		case windowCancelled, windowFailed:
			return summary, result.Err
		}
		if progress != nil {
			percent := int(windowEnd.Sub(start) * 100 / end.Sub(start))
			progress(ImportProgress{Percent: percent, ImportSummary: summary})
		}
		windowStart = windowEnd
	}
	return summary, nil
}

func countHistoricalWindows(start, end time.Time, interval time.Duration) int {
	if interval <= 0 || !start.Before(end) {
		return 0
	}
	duration := end.Sub(start)
	windows := duration / interval
	if duration%interval != 0 {
		windows++
	}
	return int(windows)
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
	step := service.Interval
	if windowDuration := end.Sub(start); windowDuration < step {
		step = windowDuration
	}
	loadSamples, err := QueryPrometheusRangeSamples(ctx, c.client, service.PrometheusURL, service.LoadQuery, start, end, step)
	if err != nil {
		return prometheusQueryFailure("load", service.LoadQuery, err)
	}
	latencySamples, err := QueryPrometheusRangeSamples(ctx, c.client, service.PrometheusURL, service.LatencyQuery, start, end, step)
	if err != nil {
		return prometheusQueryFailure("latency", service.LatencyQuery, err)
	}
	if len(loadSamples) == 0 || len(latencySamples) == 0 {
		return errWindowHasNoMetrics
	}
	if err := c.writeIndicatorChunk(service, LoadLatencyIndicator, end, loadSamples, latencySamples); err != nil {
		return err
	}
	todSamples := []ecdf.Sample{{Value: utcTimeOfDay(end), Count: 1}}
	if err := c.writeIndicatorChunk(service, TimeOfDayIndicator, end, todSamples, loadSamples); err != nil {
		return err
	}
	if c.analyzer != nil {
		requests := []*AnalysisRequest{{
			Service:     *service,
			IndicatorID: LoadLatencyIndicator,
			Timestamp:   end,
			Independent: loadSamples,
			Dependent:   latencySamples,
			Historical:  historical,
		}, {
			Service:     *service,
			IndicatorID: TimeOfDayIndicator,
			Timestamp:   end,
			Independent: todSamples,
			Dependent:   loadSamples,
			Historical:  historical,
		}}
		for _, request := range requests {
			if submitErr := c.submitAnalysis(ctx, request); submitErr != nil {
				if historical {
					return fmt.Errorf("queue historical indicator %d analysis: %w", request.IndicatorID, submitErr)
				}
				slog.Error("failed to queue analysis", "service_id", service.Id,
					"indicator_id", request.IndicatorID, "timestamp", end, "error", submitErr)
			}
		}
	}
	return nil
}

func prometheusQueryFailure(metric, query string, err error) error {
	return fmt.Errorf("Prometheus %s query %q failed: %w", metric, query, err)
}

func (c *collector) submitAnalysis(ctx context.Context, request *AnalysisRequest) error {
	if request.Historical {
		if queue, ok := c.analyzer.(contextualAnalysisQueue); ok {
			return queue.SubmitContext(ctx, request)
		}
	}
	return c.analyzer.Submit(request)
}

func utcTimeOfDay(t time.Time) float64 {
	sec := t.Unix() % 86400
	if sec < 0 {
		sec += 86400
	}
	return float64(sec)
}

func (c *collector) writeIndicatorChunk(service *config.Service, indicatorID int, timestamp time.Time, x, y []ecdf.Sample) error {
	chunk, err := ecdf.Encode(timestamp, x, y)
	if err != nil {
		return err
	}
	return c.chunkStore.WriteChunk(service.Id, indicatorID, service.Generation, timestamp, chunk)
}

func (c *collector) emit(serviceID int, kind, message string) {
	event := CollectorEvent{ServiceID: serviceID, Kind: kind, Message: message, At: time.Now().UTC()}
	attrs := []slog.Attr{
		slog.Int("service_id", serviceID),
		slog.String("event", kind),
		slog.String("message", message),
	}
	level := slog.LevelInfo
	if kind == "collection_failed" || kind == "collection_delayed" {
		level = slog.LevelWarn
	}
	slog.Default().LogAttrs(context.Background(), level, "collector event", attrs...)
	if c.events != nil {
		c.events(event)
	}
}
