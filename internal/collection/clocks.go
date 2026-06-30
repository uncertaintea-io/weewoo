package collection

import (
	"sync"
	"time"
)

type SchedulerClock interface {
	Now() time.Time
	NewTimer(time.Duration) SchedulerTimer
}

type SchedulerTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type realClock struct{}

func NewRealClock() SchedulerClock {
	return &realClock{}
}

func (realClock) Now() time.Time {
	return time.Now()
}

func (realClock) NewTimer(d time.Duration) SchedulerTimer {
	return realTimer{Timer: time.NewTimer(d)}
}

type realTimer struct {
	*time.Timer
}

func (t realTimer) C() <-chan time.Time {
	return t.Timer.C
}

func (t realTimer) Stop() bool {
	if t.Timer == nil {
		return false
	}
	active := !t.Timer.Stop()
	return active
}

type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*FakeTimer
}

func NewFakeClock(now time.Time) *FakeClock {
	return &FakeClock{now: now}
}

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *FakeClock) NewTimer(d time.Duration) SchedulerTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &FakeTimer{
		ch:  make(chan time.Time, 1),
		due: c.now.Add(d),
	}
	c.timers = append(c.timers, timer)
	if !timer.due.After(c.now) {
		timer.fire(c.now)
	}
	return timer
}

func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	timers := append([]*FakeTimer(nil), c.timers...)
	c.mu.Unlock()

	for _, timer := range timers {
		timer.fireIfDue(now)
	}
}

type FakeTimer struct {
	mu      sync.Mutex
	ch      chan time.Time
	due     time.Time
	stopped bool
	fired   bool
}

func (t *FakeTimer) C() <-chan time.Time {
	return t.ch
}

func (t *FakeTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	active := !t.stopped && !t.fired
	t.stopped = true
	return active
}

func (t *FakeTimer) fireIfDue(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped || t.fired || t.due.After(now) {
		return
	}
	t.fired = true
	select {
	case t.ch <- now:
	default:
	}
}

func (t *FakeTimer) fire(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped || t.fired {
		return
	}
	t.fired = true
	select {
	case t.ch <- now:
	default:
	}
}
