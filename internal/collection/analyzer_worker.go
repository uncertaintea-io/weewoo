package collection

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/alerting"
	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

const (
	DefaultAnalysisQueueCapacity = 64
	verdictRetryInitialDelay     = 100 * time.Millisecond
	verdictMaxAttempts           = 3
)

var (
	ErrAnalysisQueueFull  = errors.New("analysis queue is full")
	ErrAnalyzerStopped    = errors.New("sample analyzer is stopped")
	errVerdictPersistence = errors.New("verdict persistence failed")
)

type AnalysisRequest struct {
	Service     config.Service
	IndicatorID int
	Timestamp   time.Time
	Loads       []ecdf.Sample
	Latencies   []ecdf.Sample
	Historical  bool
	// Observations preserves timestamped load values for the time-of-day model.
	Observations []LoadObservation
	// VerdictTimestamps are the singleton indicator chunks governed by this analysis.
	VerdictTimestamps []time.Time
}

type LoadObservation struct {
	Timestamp time.Time
	Value     float64
}

type AnalysisQueue interface {
	Submit(AnalysisRequest) error
}

type contextualAnalysisQueue interface {
	SubmitContext(context.Context, AnalysisRequest) error
}

// AnalysisWorker evaluates samples from a bounded background queue.
type AnalysisWorker struct {
	cfg         config.Config
	jointStore  ecdf.JointStore
	chunks      ecdf.ChunkStore
	alerts      alerting.AnalysisRecorder
	liveJobs    chan AnalysisRequest
	historyJobs chan AnalysisRequest
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	stopOnce    sync.Once
	timeOfDay   map[int][]LoadObservation
}

func NewAnalysisWorker(cfg config.Config, jointStore ecdf.JointStore, chunks ecdf.ChunkStore, alerts alerting.AnalysisRecorder, capacity int) *AnalysisWorker {
	if capacity <= 0 {
		capacity = DefaultAnalysisQueueCapacity
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker := &AnalysisWorker{
		cfg:         cfg,
		jointStore:  jointStore,
		chunks:      chunks,
		alerts:      alerts,
		liveJobs:    make(chan AnalysisRequest, capacity),
		historyJobs: make(chan AnalysisRequest, capacity),
		ctx:         ctx,
		cancel:      cancel,
		done:        make(chan struct{}),
		timeOfDay:   make(map[int][]LoadObservation),
	}
	go worker.run()
	return worker
}

// Submit queues analysis without waiting for it to finish.
func (w *AnalysisWorker) Submit(request AnalysisRequest) error {
	select {
	case <-w.done:
		return ErrAnalyzerStopped
	default:
	}

	request = cloneAnalysisRequest(request)
	jobs := w.liveJobs
	if request.Historical {
		jobs = w.historyJobs
	}
	select {
	case jobs <- request:
		return nil
	case <-w.done:
		return ErrAnalyzerStopped
	default:
		return ErrAnalysisQueueFull
	}
}

// SubmitContext applies backpressure until historical analysis capacity is
// available, the caller cancels, or the worker stops.
func (w *AnalysisWorker) SubmitContext(ctx context.Context, request AnalysisRequest) error {
	request = cloneAnalysisRequest(request)
	jobs := w.liveJobs
	if request.Historical {
		jobs = w.historyJobs
	}
	select {
	case jobs <- request:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-w.done:
		return ErrAnalyzerStopped
	}
}

func cloneAnalysisRequest(request AnalysisRequest) AnalysisRequest {
	request.Loads = slices.Clone(request.Loads)
	request.Latencies = slices.Clone(request.Latencies)
	request.Observations = slices.Clone(request.Observations)
	request.VerdictTimestamps = slices.Clone(request.VerdictTimestamps)
	return request
}

func (w *AnalysisWorker) Stop() {
	w.stopOnce.Do(w.cancel)
	<-w.done
}

func (w *AnalysisWorker) run() {
	defer close(w.done)
	for {
		// Prefer a live chunk whenever one is already waiting. Historical work
		// still progresses whenever the live queue is empty.
		select {
		case <-w.ctx.Done():
			return
		case request := <-w.liveJobs:
			w.analyze(request)
			continue
		default:
		}
		select {
		case <-w.ctx.Done():
			return
		case request := <-w.liveJobs:
			w.analyze(request)
		case request := <-w.historyJobs:
			w.analyze(request)
		}
	}
}

func (w *AnalysisWorker) analyze(request AnalysisRequest) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error(
				"panic while analyzing sample",
				"panic", recovered,
				"service_id", request.Service.Id,
				"indicator_id", request.IndicatorID,
				"timestamp", request.Timestamp,
			)
		}
	}()

	var timeOfDayObservations []LoadObservation
	if request.IndicatorID == TimeOfDayIndicator {
		w.timeOfDay[request.Service.Id] = append(w.timeOfDay[request.Service.Id], request.Observations...)
		cutoff := request.Timestamp.Add(-5 * time.Minute)
		observations := w.timeOfDay[request.Service.Id]
		first := 0
		for first < len(observations) && observations[first].Timestamp.Before(cutoff) {
			first++
		}
		w.timeOfDay[request.Service.Id] = slices.Clone(observations[first:])
		timeOfDayObservations = w.timeOfDay[request.Service.Id]
	}
	retryDelay := verdictRetryInitialDelay
	for attempt := 1; attempt <= verdictMaxAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(w.ctx, analyzeSampleTimeout)
		var err error
		if request.IndicatorID == TimeOfDayIndicator {
			_, err = analyzeTimeOfDay(ctx, w.cfg, w.jointStore, w.chunks, w.alerts, &request.Service,
				request.Timestamp, timeOfDayObservations, request.VerdictTimestamps, request.Historical)
		} else {
			_, err = analyzeSample(
				ctx,
				w.cfg,
				w.jointStore,
				w.chunks,
				w.alerts,
				&request.Service,
				request.IndicatorID,
				request.Timestamp,
				request.Loads,
				request.Latencies,
				request.Historical,
			)
		}
		cancel()
		if err == nil {
			return
		}
		if !errors.Is(err, errVerdictPersistence) {
			slog.Error(
				"failed to analyze sample",
				"service_id", request.Service.Id,
				"indicator_id", request.IndicatorID,
				"timestamp", request.Timestamp,
				"error", err,
			)
			if w.alerts != nil && !errors.Is(err, context.Canceled) {
				recordCtx, recordCancel := context.WithTimeout(w.ctx, analyzeSampleTimeout)
				recordErr := w.alerts.RecordAnalysisFailure(recordCtx, alerting.AnalysisOutcome{
					ServiceID: request.Service.Id, ServiceName: request.Service.Name,
					IndicatorID: request.IndicatorID, Timestamp: request.Timestamp,
					Historical: request.Historical,
				}, err)
				recordCancel()
				if recordErr != nil {
					slog.Error("failed to record analysis impairment", "error", recordErr)
				}
			}
			return
		}
		if attempt == verdictMaxAttempts {
			slog.Error(
				"failed to persist chunk verdict after retries",
				"service_id", request.Service.Id,
				"indicator_id", request.IndicatorID,
				"timestamp", request.Timestamp,
				"attempts", attempt,
				"error", err,
			)
			return
		}

		slog.Warn(
			"failed to persist chunk verdict; retrying analysis",
			"service_id", request.Service.Id,
			"indicator_id", request.IndicatorID,
			"timestamp", request.Timestamp,
			"retry_delay", retryDelay,
			"error", err,
		)
		timer := time.NewTimer(retryDelay)
		select {
		case <-w.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		retryDelay *= 2
	}
}
