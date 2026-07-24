package alerting

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/config"
)

const (
	DefaultQueueCapacity = 64
	deliveryTimeout      = 5 * time.Second
)

var (
	ErrQueueFull         = errors.New("alert queue is full")
	ErrDispatcherStopped = errors.New("alert dispatcher is stopped")
)

type sendAlert func(context.Context, config.Config, AlertingOptions) error

// Dispatcher delivers alerts from a bounded background queue.
type Dispatcher struct {
	cfg      config.Config
	send     sendAlert
	jobs     chan AlertingOptions
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	stopOnce sync.Once
}

func NewDispatcher(cfg config.Config, capacity int) *Dispatcher {
	return newDispatcher(cfg, capacity, SendItContext)
}

func newDispatcher(cfg config.Config, capacity int, send sendAlert) *Dispatcher {
	if capacity <= 0 {
		capacity = DefaultQueueCapacity
	}
	if send == nil {
		send = SendItContext
	}
	ctx, cancel := context.WithCancel(context.Background())
	dispatcher := &Dispatcher{
		cfg:    cfg,
		send:   send,
		jobs:   make(chan AlertingOptions, capacity),
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go dispatcher.run()
	return dispatcher
}

// Submit queues an alert without waiting for network delivery.
func (d *Dispatcher) Submit(options AlertingOptions) error {
	select {
	case <-d.done:
		return ErrDispatcherStopped
	default:
	}

	select {
	case d.jobs <- options:
		return nil
	case <-d.done:
		return ErrDispatcherStopped
	default:
		return ErrQueueFull
	}
}

func (d *Dispatcher) Stop() {
	d.stopOnce.Do(d.cancel)
	<-d.done
}

func (d *Dispatcher) run() {
	defer close(d.done)
	for {
		select {
		case <-d.ctx.Done():
			return
		case options := <-d.jobs:
			d.deliver(options)
		}
	}
}

func (d *Dispatcher) deliver(options AlertingOptions) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("panic while delivering alert", "panic", recovered, "alert_name", options.AlertName)
		}
	}()

	ctx, cancel := context.WithTimeout(d.ctx, deliveryTimeout)
	defer cancel()
	if err := d.send(ctx, d.cfg, options); err != nil {
		slog.Error("failed to deliver alert", "alert_name", options.AlertName, "service", options.Service, "error", err)
	}
}
