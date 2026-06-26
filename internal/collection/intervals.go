package collection

import (
	"errors"
	"slices"
	"sync"
	"time"
)

type IntervalFunction func(start time.Time, end time.Time)

type callback struct {
	interval time.Duration
	fn       IntervalFunction
	next     time.Time
}

func (c *callback) Compare(other *callback) int {
	return c.next.Compare(other.next)
}

type IntervalScheduler struct {
	mu        sync.Mutex
	callbacks []*callback
	stopped   bool
	wake      chan struct{}
}

func NewIntervalScheduler() *IntervalScheduler {
	is := &IntervalScheduler{
		callbacks: []*callback{},
		wake:      make(chan struct{}, 1),
	}
	go is.run()
	return is
}

func (s *IntervalScheduler) AddCallback(interval time.Duration, fn IntervalFunction) error {
	if interval <= 0 {
		return errors.New("interval must be positive")
	}

	now := time.Now()
	next := now.Truncate(interval)
	if !next.Equal(now) {
		next = next.Add(interval)
	}
	cb := &callback{
		interval: interval,
		fn:       fn,
		next:     next,
	}

	s.mu.Lock()
	s.insertLocked(cb)
	s.mu.Unlock()
	s.signal()

	return nil
}

func (s *IntervalScheduler) insertLocked(cb *callback) {
	i, _ := slices.BinarySearchFunc(s.callbacks, cb, (*callback).Compare)
	s.callbacks = slices.Insert(s.callbacks, i, cb)
}

func (s *IntervalScheduler) Stop() {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	s.signal()
}

func (s *IntervalScheduler) run() {
	s.mu.Lock()
	for {
		if s.stopped {
			s.mu.Unlock()
			return
		}
		if len(s.callbacks) == 0 {
			s.mu.Unlock()
			<-s.wake
			s.mu.Lock()
			continue
		}

		cb := s.callbacks[0]
		wait := time.Until(cb.next)
		if wait > 0 {
			s.mu.Unlock()
			select {
			case <-time.After(wait):
			case <-s.wake:
			}
			s.mu.Lock()
			continue
		}

		// Remove the callback from the list before releasing the lock so that
		// we don't corrupt the sort order when modifying the next execution time.
		s.callbacks = slices.Delete(s.callbacks, 0, 1)

		// Release the mutex before executing the callback to avoid blocking other operations.
		s.mu.Unlock()

		// Run the callback and schedule the next execution time.
		end := cb.next
		cb.fn(end.Add(-cb.interval), end)
		cb.next = end.Add(cb.interval)

		// Re-insert the callback with the updated next execution time.
		s.mu.Lock()
		s.insertLocked(cb)
	}
}

func (s *IntervalScheduler) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
