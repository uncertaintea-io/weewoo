package collection

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testWindow struct {
	id    int
	start time.Time
	end   time.Time
}

type fixedBackoff time.Duration

func (b fixedBackoff) Delay(time.Duration, int) time.Duration {
	return time.Duration(b)
}

func newTestScheduler(clock *FakeClock, options ...SchedulerOption) *IntervalScheduler {
	opts := []SchedulerOption{
		WithSchedulerClock(clock),
		WithSchedulerEventHandler(nil),
		WithSchedulerBackoff(fixedBackoff(time.Second)),
	}
	opts = append(opts, options...)
	return NewIntervalScheduler(opts...)
}

func waitWindow(t *testing.T, windows <-chan testWindow) testWindow {
	t.Helper()
	select {
	case window := <-windows:
		return window
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for window")
		return testWindow{}
	}
}

func assertNoWindow(t *testing.T, windows <-chan testWindow) {
	t.Helper()
	select {
	case window := <-windows:
		t.Fatalf("unexpected window: %#v", window)
	case <-time.After(25 * time.Millisecond):
	}
}

func TestIntervalSchedulerColdStartUsesNextBoundary(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 1, 1, 12, 0, 30, 0, time.UTC))
	scheduler := newTestScheduler(clock)
	defer scheduler.Stop()

	windows := make(chan testWindow, 1)
	require.NoError(t, scheduler.AddCallback(1, time.Minute, func(ctx context.Context, start time.Time, end time.Time) IntervalResult {
		windows <- testWindow{id: 1, start: start, end: end}
		return IntervalSuccess()
	}))

	assertNoWindow(t, windows)
	clock.Advance(30 * time.Second)

	window := waitWindow(t, windows)
	assert.Equal(t, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), window.start)
	assert.Equal(t, time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC), window.end)
}

func TestIntervalSchedulerRealignsWindowsOnIntervalUpdate(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC))
	scheduler := newTestScheduler(clock)
	defer scheduler.Stop()

	windows := make(chan testWindow, 3)
	fn := func(ctx context.Context, start time.Time, end time.Time) IntervalResult {
		windows <- testWindow{id: 1, start: start, end: end}
		return IntervalSuccess()
	}
	require.NoError(t, scheduler.AddCallback(1, time.Minute, fn, WithLastEnd(clock.Now())))
	require.NoError(t, scheduler.AddCallback(1, 2*time.Minute, fn))

	clock.Advance(2 * time.Minute)
	first := waitWindow(t, windows)
	assert.Equal(t, time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC), first.start)
	assert.Equal(t, time.Date(2026, 1, 1, 12, 2, 0, 0, time.UTC), first.end)

	clock.Advance(time.Minute)
	second := waitWindow(t, windows)
	assert.Equal(t, time.Date(2026, 1, 1, 12, 2, 0, 0, time.UTC), second.start)
	assert.Equal(t, time.Date(2026, 1, 1, 12, 4, 0, 0, time.UTC), second.end)

	clock.Advance(2 * time.Minute)
	third := waitWindow(t, windows)
	assert.Equal(t, time.Date(2026, 1, 1, 12, 4, 0, 0, time.UTC), third.start)
	assert.Equal(t, time.Date(2026, 1, 1, 12, 6, 0, 0, time.UTC), third.end)
}

func TestIntervalSchedulerRetryDoesNotBlockOtherCallbackIDs(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	scheduler := newTestScheduler(clock)
	defer scheduler.Stop()

	windows := make(chan testWindow, 4)
	failOnce := true
	require.NoError(t, scheduler.AddCallback(1, time.Minute, func(ctx context.Context, start time.Time, end time.Time) IntervalResult {
		windows <- testWindow{id: 1, start: start, end: end}
		if failOnce {
			failOnce = false
			return IntervalRetry(errors.New("temporary failure"))
		}
		return IntervalSuccess()
	}, WithLastEnd(clock.Now())))
	require.NoError(t, scheduler.AddCallback(2, time.Minute, func(ctx context.Context, start time.Time, end time.Time) IntervalResult {
		windows <- testWindow{id: 2, start: start, end: end}
		return IntervalSuccess()
	}, WithLastEnd(clock.Now())))

	clock.Advance(time.Minute)
	first := waitWindow(t, windows)
	second := waitWindow(t, windows)
	seen := map[int]bool{first.id: true, second.id: true}
	assert.True(t, seen[1], "callback 1 should have run")
	assert.True(t, seen[2], "callback 2 should have run")

	clock.Advance(time.Second)
	retry := waitWindow(t, windows)
	assert.Equal(t, 1, retry.id)
	assert.Equal(t, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), retry.start)
	assert.Equal(t, time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC), retry.end)

	clock.Advance(time.Minute)
	nextA := waitWindow(t, windows)
	nextB := waitWindow(t, windows)
	seenNext := map[int]testWindow{nextA.id: nextA, nextB.id: nextB}
	require.Contains(t, seenNext, 2)
	assert.Equal(t, time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC), seenNext[2].start)
	assert.Equal(t, time.Date(2026, 1, 1, 12, 2, 0, 0, time.UTC), seenNext[2].end)
}

func TestIntervalSchedulerPermanentFailureResumesFromLastEndOnUpdate(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	scheduler := newTestScheduler(clock)
	defer scheduler.Stop()

	windows := make(chan testWindow, 2)
	require.NoError(t, scheduler.AddCallback(1, time.Minute, func(ctx context.Context, start time.Time, end time.Time) IntervalResult {
		windows <- testWindow{id: 1, start: start, end: end}
		return IntervalPermanent(errors.New("bad config"))
	}, WithLastEnd(clock.Now())))

	clock.Advance(time.Minute)
	failed := waitWindow(t, windows)
	assert.Equal(t, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), failed.start)
	assert.Equal(t, time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC), failed.end)

	clock.Advance(time.Minute)
	assertNoWindow(t, windows)

	require.NoError(t, scheduler.AddCallback(1, 2*time.Minute, func(ctx context.Context, start time.Time, end time.Time) IntervalResult {
		windows <- testWindow{id: 1, start: start, end: end}
		return IntervalSuccess()
	}))
	clock.Advance(2 * time.Minute)
	resumed := waitWindow(t, windows)
	assert.Equal(t, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), resumed.start)
	assert.Equal(t, time.Date(2026, 1, 1, 12, 2, 0, 0, time.UTC), resumed.end)
}

func TestIntervalSchedulerStopCancelsAndWaitsForInFlightCallbacks(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	scheduler := newTestScheduler(clock)

	started := make(chan struct{})
	done := make(chan struct{})
	require.NoError(t, scheduler.AddCallback(1, time.Minute, func(ctx context.Context, start time.Time, end time.Time) IntervalResult {
		close(started)
		<-ctx.Done()
		close(done)
		return IntervalRetry(ctx.Err())
	}, WithLastEnd(clock.Now())))

	clock.Advance(time.Minute)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for callback start")
	}

	stopped := make(chan struct{})
	go func() {
		scheduler.Stop()
		close(stopped)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for callback cancellation")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scheduler stop")
	}

	err := scheduler.AddCallback(2, time.Minute, func(context.Context, time.Time, time.Time) IntervalResult {
		return IntervalSuccess()
	})
	assert.ErrorIs(t, err, ErrSchedulerStopped)
}
