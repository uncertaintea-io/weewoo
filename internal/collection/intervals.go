// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

package collection

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"
)

var (
	ErrInvalidInterval  = errors.New("invalid interval")
	ErrSchedulerStopped = errors.New("interval scheduler stopped")
	ErrNilCallback      = errors.New("nil interval callback")
)

type IntervalFunction func(ctx context.Context, start time.Time, end time.Time) IntervalResult

type IntervalResult struct {
	Err       error
	Retryable bool
}

func IntervalSuccess() IntervalResult {
	return IntervalResult{}
}

func IntervalRetry(err error) IntervalResult {
	if err == nil {
		err = errors.New("retryable interval callback failure")
	}
	return IntervalResult{Err: err, Retryable: true}
}

func IntervalPermanent(err error) IntervalResult {
	if err == nil {
		err = errors.New("permanent interval callback failure")
	}
	return IntervalResult{Err: err}
}

type SchedulerEventKind string

const (
	SchedulerEventCallbackAdded    SchedulerEventKind = "callback_added"
	SchedulerEventCallbackUpdated  SchedulerEventKind = "callback_updated"
	SchedulerEventCallbackRemoved  SchedulerEventKind = "callback_removed"
	SchedulerEventWindowSucceeded  SchedulerEventKind = "window_succeeded"
	SchedulerEventRetryScheduled   SchedulerEventKind = "window_retry_scheduled"
	SchedulerEventCallbackDisabled SchedulerEventKind = "callback_disabled"
	SchedulerEventCallbackResumed  SchedulerEventKind = "callback_resumed"
	SchedulerEventSchedulerStopped SchedulerEventKind = "scheduler_stopped"
	MaxBackoffDelay                time.Duration      = time.Hour
)

type SchedulerEvent struct {
	Kind     SchedulerEventKind
	ID       int
	Start    time.Time
	End      time.Time
	Interval time.Duration
	Attempt  int
	Err      error
	RetryAt  time.Time
}

type SchedulerEventHandler func(SchedulerEvent)

type BackoffPolicy interface {
	Delay(interval time.Duration, attempt int) time.Duration
}

type ExponentialBackoffPolicy struct {
	Initial    time.Duration
	Multiplier float64
	Max        time.Duration
}

func (p ExponentialBackoffPolicy) Delay(interval time.Duration, attempt int) time.Duration {
	initial := p.Initial
	if initial <= 0 {
		initial = time.Second
	}
	multiplier := p.Multiplier
	if multiplier <= 0 {
		multiplier = 2
	}
	maxDelay := p.Max
	if maxDelay <= 0 || maxDelay > MaxBackoffDelay {
		maxDelay = MaxBackoffDelay
	}
	if interval > 0 && interval < maxDelay {
		maxDelay = interval
	}
	if attempt <= 1 {
		if initial > maxDelay {
			return maxDelay
		}
		return initial
	}
	delay := float64(initial) * math.Pow(multiplier, float64(attempt-1))
	if delay >= float64(maxDelay) {
		return maxDelay
	}
	return time.Duration(delay)
}

type SchedulerOption func(*schedulerOptions)

type schedulerOptions struct {
	clock   SchedulerClock
	backoff BackoffPolicy
	events  SchedulerEventHandler
}

func WithSchedulerClock(clock SchedulerClock) SchedulerOption {
	return func(opts *schedulerOptions) {
		if clock != nil {
			opts.clock = clock
		}
	}
}

func WithSchedulerBackoff(backoff BackoffPolicy) SchedulerOption {
	return func(opts *schedulerOptions) {
		if backoff != nil {
			opts.backoff = backoff
		}
	}
}

func WithSchedulerEventHandler(handler SchedulerEventHandler) SchedulerOption {
	return func(opts *schedulerOptions) {
		opts.events = handler
	}
}

type CallbackOption func(*callbackOptions)

type callbackOptions struct {
	lastEnd    time.Time
	hasLastEnd bool
}

func WithLastEnd(t time.Time) CallbackOption {
	return func(opts *callbackOptions) {
		opts.lastEnd = t
		opts.hasLastEnd = true
	}
}

type IntervalScheduler struct {
	requests    chan schedulerRequest
	completions chan callbackCompletion
	done        chan struct{}
	stopOnce    sync.Once
}

type schedulerRequest struct {
	add    *addCallbackRequest
	remove *removeCallbackRequest
	stop   *stopSchedulerRequest
}

type addCallbackRequest struct {
	id       int
	interval time.Duration
	fn       IntervalFunction
	opts     callbackOptions
	resp     chan error
}

type removeCallbackRequest struct {
	id int
}

type stopSchedulerRequest struct {
	done chan struct{}
}

type callbackUpdate struct {
	interval  time.Duration
	fn        IntervalFunction
	maxWindow time.Duration
}

type callbackState struct {
	id          int
	interval    time.Duration
	fn          IntervalFunction
	lastEnd     time.Time
	maxWindow   time.Duration
	nextStart   time.Time
	nextEnd     time.Time
	nextAttempt time.Time
	attempt     int
	inFlight    bool
	disabled    bool
	removeAfter bool
	pending     *callbackUpdate
	cancel      context.CancelFunc
}

type callbackCompletion struct {
	id     int
	start  time.Time
	end    time.Time
	result IntervalResult
}

func NewIntervalScheduler(options ...SchedulerOption) *IntervalScheduler {
	opts := schedulerOptions{
		clock:   newRealClock(),
		backoff: ExponentialBackoffPolicy{},
	}
	for _, option := range options {
		option(&opts)
	}

	s := &IntervalScheduler{
		requests:    make(chan schedulerRequest),
		completions: make(chan callbackCompletion),
		done:        make(chan struct{}),
	}
	go s.run(opts)
	return s
}

func (s *IntervalScheduler) AddCallback(id int, interval time.Duration, fn IntervalFunction, options ...CallbackOption) error {
	if interval <= 0 {
		return ErrInvalidInterval
	}
	if fn == nil {
		return ErrNilCallback
	}
	opts := callbackOptions{}
	for _, option := range options {
		option(&opts)
	}

	resp := make(chan error, 1)
	req := schedulerRequest{add: &addCallbackRequest{
		id:       id,
		interval: interval,
		fn:       fn,
		opts:     opts,
		resp:     resp,
	}}
	select {
	case s.requests <- req:
	case <-s.done:
		return ErrSchedulerStopped
	}

	select {
	case err := <-resp:
		return err
	case <-s.done:
		return ErrSchedulerStopped
	}
}

func (s *IntervalScheduler) RemoveCallback(id int) {
	req := schedulerRequest{remove: &removeCallbackRequest{id: id}}
	select {
	case s.requests <- req:
	case <-s.done:
	}
}

func (s *IntervalScheduler) Stop() {
	s.stopOnce.Do(func() {
		stopped := make(chan struct{})
		req := schedulerRequest{stop: &stopSchedulerRequest{done: stopped}}
		select {
		case s.requests <- req:
			<-stopped
		case <-s.done:
		}
	})
	<-s.done
}

func (s *IntervalScheduler) run(opts schedulerOptions) {
	states := map[int]*callbackState{}
	var stopReq *stopSchedulerRequest
	var timer SchedulerTimer
	var timerC <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
		emit(opts, SchedulerEvent{Kind: SchedulerEventSchedulerStopped})
		close(s.done)
		if stopReq != nil {
			close(stopReq.done)
		}
	}()

	resetTimer := func() {
		if timer != nil {
			timer.Stop()
			timer = nil
			timerC = nil
		}
		next, ok := nextReadyAt(states)
		if !ok {
			return
		}
		delay := time.Until(next)
		if opts.clock != nil {
			delay = next.Sub(opts.clock.Now())
		}
		if delay < 0 {
			delay = 0
		}
		timer = opts.clock.NewTimer(delay)
		timerC = timer.C()
	}

	dispatchDue := func() {
		now := opts.clock.Now()
		for _, state := range states {
			if state.disabled || state.inFlight || state.nextAttempt.After(now) || state.nextEnd.After(now) {
				continue
			}
			dispatchCallback(s.completions, state)
		}
	}

	for {
		if stopReq == nil {
			dispatchDue()
		}
		if stopReq != nil && noInFlight(states) {
			return
		}
		if stopReq == nil {
			resetTimer()
		}

		select {
		case req := <-s.requests:
			if req.add != nil {
				if stopReq != nil {
					req.add.resp <- ErrSchedulerStopped
					continue
				}
				handleAdd(states, opts, req.add)
				resetTimer()
			}
			if req.remove != nil {
				handleRemove(states, opts, req.remove.id)
				resetTimer()
			}
			if req.stop != nil && stopReq == nil {
				stopReq = req.stop
				for _, state := range states {
					if state.cancel != nil {
						state.cancel()
					}
				}
			}
		case completion := <-s.completions:
			handleCompletion(states, opts, completion)
			resetTimer()
		case <-timerC:
			timer = nil
			timerC = nil
		}
	}
}

func handleAdd(states map[int]*callbackState, opts schedulerOptions, req *addCallbackRequest) {
	existing, ok := states[req.id]
	if !ok {
		lastEnd := req.opts.lastEnd
		if !req.opts.hasLastEnd {
			next := nextBoundary(opts.clock.Now(), req.interval)
			lastEnd = next.Add(-req.interval)
		}
		state := &callbackState{
			id:        req.id,
			interval:  req.interval,
			fn:        req.fn,
			lastEnd:   lastEnd,
			maxWindow: req.interval,
		}
		planNextWindow(state, opts.clock.Now())
		states[req.id] = state
		emit(opts, SchedulerEvent{Kind: SchedulerEventCallbackAdded, ID: req.id, Interval: req.interval})
		req.resp <- nil
		return
	}

	update := callbackUpdate{
		interval:  req.interval,
		fn:        req.fn,
		maxWindow: maxDuration(existing.interval, req.interval),
	}
	if existing.inFlight {
		existing.pending = &update
	} else {
		wasDisabled := existing.disabled
		applyUpdate(existing, update, opts.clock.Now())
		if wasDisabled {
			emit(opts, SchedulerEvent{Kind: SchedulerEventCallbackResumed, ID: req.id, Interval: req.interval})
		} else {
			emit(opts, SchedulerEvent{Kind: SchedulerEventCallbackUpdated, ID: req.id, Interval: req.interval})
		}
	}
	req.resp <- nil
}

func handleRemove(states map[int]*callbackState, opts schedulerOptions, id int) {
	state, ok := states[id]
	if !ok {
		return
	}
	if state.cancel != nil {
		state.cancel()
	}
	if state.inFlight {
		state.removeAfter = true
	} else {
		delete(states, id)
	}
	emit(opts, SchedulerEvent{Kind: SchedulerEventCallbackRemoved, ID: id, Interval: state.interval})
}

func handleCompletion(states map[int]*callbackState, opts schedulerOptions, completion callbackCompletion) {
	state, ok := states[completion.id]
	if !ok {
		return
	}
	state.inFlight = false
	state.cancel = nil
	if state.removeAfter {
		delete(states, state.id)
		return
	}

	if completion.result.Err == nil {
		state.lastEnd = completion.end
		state.attempt = 0
		emit(opts, SchedulerEvent{
			Kind: SchedulerEventWindowSucceeded,
			ID:   state.id, Start: completion.start, End: completion.end,
			Interval: state.interval,
		})
		if state.pending != nil {
			applyUpdate(state, *state.pending, opts.clock.Now())
			emit(opts, SchedulerEvent{Kind: SchedulerEventCallbackUpdated, ID: state.id, Interval: state.interval})
		} else {
			planNextWindow(state, opts.clock.Now())
		}
		return
	}

	if state.pending != nil {
		applyUpdate(state, *state.pending, opts.clock.Now())
		emit(opts, SchedulerEvent{Kind: SchedulerEventCallbackUpdated, ID: state.id, Interval: state.interval})
		return
	}

	if completion.result.Retryable {
		state.attempt++
		delay := opts.backoff.Delay(state.interval, state.attempt)
		state.nextStart = completion.start
		state.nextEnd = completion.end
		state.nextAttempt = opts.clock.Now().Add(delay)
		emit(opts, SchedulerEvent{
			Kind: SchedulerEventRetryScheduled,
			ID:   state.id, Start: completion.start, End: completion.end,
			Interval: state.interval, Attempt: state.attempt, Err: completion.result.Err,
			RetryAt: state.nextAttempt,
		})
		return
	}

	state.disabled = true
	emit(opts, SchedulerEvent{
		Kind: SchedulerEventCallbackDisabled,
		ID:   state.id, Start: completion.start, End: completion.end,
		Interval: state.interval, Err: completion.result.Err,
	})
}

func applyUpdate(state *callbackState, update callbackUpdate, now time.Time) {
	state.interval = update.interval
	state.fn = update.fn
	state.maxWindow = update.maxWindow
	state.pending = nil
	state.disabled = false
	state.attempt = 0
	state.nextAttempt = time.Time{}
	planNextWindow(state, now)
}

func planNextWindow(state *callbackState, now time.Time) {
	start := state.lastEnd
	boundary := nextBoundary(start, state.interval)
	end := start.Add(state.interval)
	if !start.Equal(boundary) {
		cap := state.maxWindow
		if cap <= 0 {
			cap = state.interval
		}
		end = start.Add(cap)
		if end.After(boundary) {
			end = boundary
		}
	} else if end.Before(now) {
		// Keep the same one-window step. The run loop will dispatch catch-up
		// windows immediately while each computed end remains in the past.
	}
	state.nextStart = start
	state.nextEnd = end
	state.nextAttempt = time.Time{}
}

func dispatchCallback(completions chan<- callbackCompletion, state *callbackState) {
	ctx, cancel := context.WithCancel(context.Background())
	state.inFlight = true
	state.cancel = cancel
	start := state.nextStart
	end := state.nextEnd
	fn := state.fn
	id := state.id
	go func() {
		result := func() (result IntervalResult) {
			defer func() {
				if recovered := recover(); recovered != nil {
					err := fmt.Errorf("interval callback %d panicked: %v", id, recovered)
					slog.Error("interval callback panic", "callback_id", id, "error", err)
					result = IntervalRetry(err)
				}
			}()
			return fn(ctx, start, end)
		}()
		if ctx.Err() != nil {
			result = IntervalPermanent(ctx.Err())
		}
		completions <- callbackCompletion{id: id, start: start, end: end, result: result}
	}()
}

func nextReadyAt(states map[int]*callbackState) (time.Time, bool) {
	var next time.Time
	ok := false
	for _, state := range states {
		if state.disabled || state.inFlight {
			continue
		}
		readyAt := state.nextEnd
		if state.nextAttempt.After(readyAt) {
			readyAt = state.nextAttempt
		}
		if !ok || readyAt.Before(next) {
			next = readyAt
			ok = true
		}
	}
	return next, ok
}

func noInFlight(states map[int]*callbackState) bool {
	for _, state := range states {
		if state.inFlight {
			return false
		}
	}
	return true
}

func nextBoundary(t time.Time, interval time.Duration) time.Time {
	boundary := t.Truncate(interval)
	if !boundary.Equal(t) {
		boundary = boundary.Add(interval)
	}
	return boundary
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func emit(opts schedulerOptions, event SchedulerEvent) {
	logSchedulerEvent(event)
	if opts.events != nil {
		opts.events(event)
	}
}

func logSchedulerEvent(event SchedulerEvent) {
	attrs := []slog.Attr{
		slog.String("event", string(event.Kind)),
	}
	if event.ID != 0 {
		attrs = append(attrs, slog.Int("callback_id", event.ID))
	}
	if !event.Start.IsZero() {
		attrs = append(attrs, slog.Time("start", event.Start))
	}
	if !event.End.IsZero() {
		attrs = append(attrs, slog.Time("end", event.End))
	}
	if event.Interval != 0 {
		attrs = append(attrs, slog.Duration("interval", event.Interval))
	}
	if event.Attempt != 0 {
		attrs = append(attrs, slog.Int("attempt", event.Attempt))
	}
	if !event.RetryAt.IsZero() {
		attrs = append(attrs, slog.Time("retry_at", event.RetryAt))
	}
	if event.Err != nil {
		attrs = append(attrs, slog.Any("error", event.Err))
		slog.Default().LogAttrs(context.Background(), slog.LevelWarn, "interval scheduler event", attrs...)
		return
	}
	slog.Default().LogAttrs(context.Background(), slog.LevelInfo, "interval scheduler event", attrs...)
}
