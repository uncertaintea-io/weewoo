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
	LoadLatencyIndicator  = 1
	TimeOfDayIndicator    = 2
	historicalBatchPoints = 10_000
	historicalBatchSpan   = time.Duration(historicalBatchPoints-1) * 15 * time.Second
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
	GapWindows      int
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

// Import collects an explicit historical window for a service.
func (c *collector) Import(ctx context.Context, service *config.Service, start, end time.Time, progress ImportProgressHandler) (ImportSummary, error) {
	summary := ImportSummary{TotalWindows: countHistoricalWindows(start, end, service.Interval)}
	if service.Interval <= 0 {
		return summary, fmt.Errorf("invalid service interval")
	}
	processor := newWindowProcessor(time.Now)
	windows := newHistoricalWindows(start, end, service.Interval)
	for batchStart := start; batchStart.Before(end); {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		batchEnd := batchStart.Add(historicalBatchSpan)
		if batchEnd.After(end) {
			batchEnd = end
		}
		loadPoints, err := queryPrometheusRangePointsAtStep(ctx, c.client, service.PrometheusURL, service.LoadQuery, batchStart, batchEnd, service.Interval)
		if err != nil && !errors.Is(err, ErrNoPrometheusData) {
			return summary, err
		}
		latencyPoints, err := queryPrometheusRangePointsAtStep(ctx, c.client, service.PrometheusURL, service.LatencyQuery, batchStart, batchEnd, service.Interval)
		if err != nil && !errors.Is(err, ErrNoPrometheusData) {
			return summary, err
		}
		windows.add(batchStart, start, loadPoints, latencyPoints)
		if err := windows.flushThrough(batchEnd, func(window collectionWindow, loads []prometheusPoint, latencies []float64) error {
			result := processor.Process(ctx, windowAttempt{Window: window}, func(context.Context) error {
				if len(loads) == 0 || len(latencies) == 0 {
					return errWindowHasNoMetrics
				}
				return c.writeCollectedIndicators(ctx, service, window.End, loads, latencies, true)
			}, historicalImportWindowPolicy{})
			switch result.Outcome {
			case windowCompleted:
				summary.ImportedWindows++
			case windowMonitoringGap:
				summary.GapWindows++
			case windowCancelled, windowFailed:
				return result.Err
			}
			return nil
		}); err != nil {
			return summary, err
		}
		if progress != nil {
			percent := int(batchEnd.Sub(start) * 100 / end.Sub(start))
			progress(ImportProgress{Percent: percent, ImportSummary: summary})
		}
		batchStart = batchEnd
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

type historicalWindowValues struct {
	loads     []prometheusPoint
	latencies []float64
}

type historicalWindows struct {
	start     time.Time
	end       time.Time
	interval  time.Duration
	nextStart time.Time
	nextFlush time.Time
	values    map[time.Time]*historicalWindowValues
}

func newHistoricalWindows(start, end time.Time, interval time.Duration) *historicalWindows {
	nextFlush := start.Add(interval)
	if nextFlush.After(end) {
		nextFlush = end
	}
	return &historicalWindows{
		start: start, end: end, interval: interval, nextStart: start, nextFlush: nextFlush,
		values: make(map[time.Time]*historicalWindowValues),
	}
}

func (w *historicalWindows) add(batchStart, importStart time.Time, loads, latencies []prometheusPoint) {
	for _, point := range loads {
		if batchStart.After(importStart) && !point.Timestamp.After(batchStart) {
			continue
		}
		if windowEnd, ok := w.windowEnd(point.Timestamp); ok {
			values := w.valueFor(windowEnd)
			values.loads = append(values.loads, point)
		}
	}
	for _, point := range latencies {
		if batchStart.After(importStart) && !point.Timestamp.After(batchStart) {
			continue
		}
		if windowEnd, ok := w.windowEnd(point.Timestamp); ok {
			values := w.valueFor(windowEnd)
			values.latencies = append(values.latencies, point.Value)
		}
	}
}

func (w *historicalWindows) windowEnd(timestamp time.Time) (time.Time, bool) {
	if timestamp.Before(w.start) || !timestamp.Before(w.end) {
		return time.Time{}, false
	}
	index := timestamp.Sub(w.start) / w.interval
	windowEnd := w.start.Add((index + 1) * w.interval)
	if windowEnd.After(w.end) {
		windowEnd = w.end
	}
	return windowEnd, true
}

func (w *historicalWindows) valueFor(windowEnd time.Time) *historicalWindowValues {
	values := w.values[windowEnd]
	if values == nil {
		values = &historicalWindowValues{}
		w.values[windowEnd] = values
	}
	return values
}

func (w *historicalWindows) flushThrough(through time.Time, write func(collectionWindow, []prometheusPoint, []float64) error) error {
	for !w.nextFlush.After(through) {
		values := w.values[w.nextFlush]
		if values == nil {
			values = &historicalWindowValues{}
		}
		if err := write(collectionWindow{Start: w.nextStart, End: w.nextFlush}, values.loads, values.latencies); err != nil {
			return err
		}
		delete(w.values, w.nextFlush)
		if w.nextFlush.Equal(w.end) {
			break
		}
		w.nextStart = w.nextFlush
		w.nextFlush = w.nextFlush.Add(w.interval)
		if w.nextFlush.After(w.end) {
			w.nextFlush = w.end
		}
	}
	return nil
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
	loadPoints, err := queryPrometheusRangePointsAtStep(ctx, c.client, service.PrometheusURL, service.LoadQuery, start, end, service.Interval)
	if err != nil {
		return err
	}
	loadPoints = pointsInWindow(loadPoints, start, end)
	latencyPoints, err := queryPrometheusRangePointsAtStep(ctx, c.client, service.PrometheusURL, service.LatencyQuery, start, end, service.Interval)
	if err != nil {
		return err
	}
	latencyPoints = pointsInWindow(latencyPoints, start, end)
	latencyValue := make([]float64, len(latencyPoints))
	for i, point := range latencyPoints {
		latencyValue[i] = point.Value
	}
	return c.writeCollectedIndicators(ctx, service, end, loadPoints, latencyValue, historical)
}

func pointsInWindow(points []prometheusPoint, start, end time.Time) []prometheusPoint {
	filtered := points[:0]
	for _, point := range points {
		if point.Timestamp.After(start) && !point.Timestamp.After(end) {
			filtered = append(filtered, point)
		}
	}
	return filtered
}

func (c *collector) writeCollectedIndicators(ctx context.Context, service *config.Service, end time.Time, loadPoints []prometheusPoint, latencyValues []float64, historical bool) error {
	loadValues := make([]float64, len(loadPoints))
	for i, point := range loadPoints {
		loadValues[i] = point.Value
	}
	loads := ecdf.CountSamples(loadValues)
	latencies := ecdf.CountSamples(latencyValues)
	if err := c.writeIndicatorChunk(service, LoadLatencyIndicator, end, loads, latencies); err != nil {
		return err
	}
	observations, err := c.writeTimeOfDayChunks(service, loadPoints)
	if err != nil {
		return err
	}
	if c.analyzer != nil {
		requests := []AnalysisRequest{{
			Service:     *service,
			IndicatorID: LoadLatencyIndicator,
			Timestamp:   end,
			Loads:       loads,
			Latencies:   latencies,
			Historical:  historical,
		}}
		if len(observations) > 0 {
			timestamps := make([]time.Time, len(observations))
			for i := range observations {
				timestamps[i] = observations[i].Timestamp
			}
			requests = append(requests, AnalysisRequest{Service: *service, IndicatorID: TimeOfDayIndicator, Timestamp: end,
				Observations: observations, ChunkTimestamps: timestamps, Historical: historical})
		}
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

func (c *collector) submitAnalysis(ctx context.Context, request AnalysisRequest) error {
	if request.Historical {
		if queue, ok := c.analyzer.(contextualAnalysisQueue); ok {
			return queue.SubmitContext(ctx, request)
		}
	}
	return c.analyzer.Submit(request)
}

func (c *collector) writeTimeOfDayChunks(service *config.Service, points []prometheusPoint) ([]Observation, error) {
	if service.Interval <= 0 {
		return nil, fmt.Errorf("invalid service interval")
	}
	observations := make([]Observation, 0, len(points))
	seen := make(map[time.Time]struct{}, len(points))
	for _, point := range points {
		timestamp := point.Timestamp.UTC().Truncate(time.Second)
		if _, duplicate := seen[timestamp]; duplicate {
			return nil, fmt.Errorf("duplicate load observation at %s", timestamp.Format(time.RFC3339))
		}
		seen[timestamp] = struct{}{}
		x := timeOfDayBucket(timestamp, service.Interval)
		if err := c.writeIndicatorChunk(service, TimeOfDayIndicator, timestamp,
			[]ecdf.Sample{{Value: x, Count: 1}}, []ecdf.Sample{{Value: point.Value, Count: 1}}); err != nil {
			return nil, err
		}
		observations = append(observations, Observation{Timestamp: timestamp, Value: point.Value})
	}
	return observations, nil
}

func (c *collector) writeIndicatorChunk(service *config.Service, indicatorID int, timestamp time.Time, x, y []ecdf.Sample) error {
	chunk, err := ecdf.Encode(timestamp, x, y)
	if err != nil {
		return err
	}
	return c.chunkStore.WriteChunk(service.Id, indicatorID, service.Generation, timestamp, chunk)
}

func timeOfDayBucket(timestamp time.Time, interval time.Duration) float64 {
	u := timestamp.UTC()
	seconds := time.Duration(u.Hour())*time.Hour + time.Duration(u.Minute())*time.Minute + time.Duration(u.Second())*time.Second
	return float64(seconds / interval)
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
