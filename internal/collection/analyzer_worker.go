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

const DefaultAnalysisQueueCapacity = 64

var (
	ErrAnalysisQueueFull = errors.New("analysis queue is full")
	ErrAnalyzerStopped   = errors.New("sample analyzer is stopped")
)

type AnalysisRequest struct {
	Service     config.Service
	IndicatorID int
	Timestamp   time.Time
	Loads       []ecdf.Sample
	Latencies   []ecdf.Sample
}

type AnalysisQueue interface {
	Submit(AnalysisRequest) error
}

type AlertQueue interface {
	Submit(alerting.AlertingOptions) error
}

// AnalysisWorker evaluates samples from a bounded background queue.
type AnalysisWorker struct {
	cfg        config.Config
	jointStore ecdf.JointStore
	alerts     AlertQueue
	jobs       chan AnalysisRequest
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	stopOnce   sync.Once
}

func NewAnalysisWorker(cfg config.Config, jointStore ecdf.JointStore, alerts AlertQueue, capacity int) *AnalysisWorker {
	if capacity <= 0 {
		capacity = DefaultAnalysisQueueCapacity
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker := &AnalysisWorker{
		cfg:        cfg,
		jointStore: jointStore,
		alerts:     alerts,
		jobs:       make(chan AnalysisRequest, capacity),
		ctx:        ctx,
		cancel:     cancel,
		done:       make(chan struct{}),
	}
	go worker.run()
	return worker
}

// Submit queues analysis without waiting for it to finish.
func (w *AnalysisWorker) Submit(request AnalysisRequest) error {
	request.Loads = slices.Clone(request.Loads)
	request.Latencies = slices.Clone(request.Latencies)

	select {
	case <-w.done:
		return ErrAnalyzerStopped
	default:
	}

	select {
	case w.jobs <- request:
		return nil
	case <-w.done:
		return ErrAnalyzerStopped
	default:
		return ErrAnalysisQueueFull
	}
}

func (w *AnalysisWorker) Stop() {
	w.stopOnce.Do(w.cancel)
	<-w.done
}

func (w *AnalysisWorker) run() {
	defer close(w.done)
	for {
		select {
		case <-w.ctx.Done():
			return
		case request := <-w.jobs:
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

	ctx, cancel := context.WithTimeout(w.ctx, analyzeSampleTimeout)
	defer cancel()
	if _, err := analyzeSample(
		ctx,
		w.cfg,
		w.jointStore,
		w.alerts,
		&request.Service,
		request.IndicatorID,
		request.Timestamp,
		request.Loads,
		request.Latencies,
	); err != nil {
		slog.Error(
			"failed to analyze sample",
			"service_id", request.Service.Id,
			"indicator_id", request.IndicatorID,
			"timestamp", request.Timestamp,
			"error", err,
		)
	}
}
