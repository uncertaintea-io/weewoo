package collection

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIntervalSchedulerStopEmpty(t *testing.T) {
	t.Parallel()
	scheduler := NewIntervalScheduler()
	scheduler.Stop()
}

func TestIntervalSchedulerStopTwice(t *testing.T) {
	t.Parallel()
	scheduler := NewIntervalScheduler()
	scheduler.Stop()
	// Stopping again should not panic
	scheduler.Stop()
}

func TestIntervalCallback(t *testing.T) {
	t.Parallel()

	const interval = time.Millisecond
	windows := make(chan [2]time.Time, 1)

	scheduler := NewIntervalScheduler()
	defer scheduler.Stop()
	scheduler.AddCallback(interval, func(start time.Time, end time.Time) {
		select {
		case windows <- [2]time.Time{start, end}:
		default:
		}
	})

	select {
	case window := <-windows:
		start := window[0]
		end := window[1]
		assert.True(t, start.Equal(start.Truncate(interval)), "start time not aligned: %s", start.Format(time.RFC3339Nano))
		assert.True(t, end.Equal(end.Truncate(interval)), "end time not aligned: %s", end.Format(time.RFC3339Nano))
		if got := end.Sub(start); got != interval {
			t.Fatalf("callback window duration = %v, want %v", got, interval)
		}

	case <-time.After(10 * time.Millisecond):
		t.Fatal("timed out waiting for callback")
	}
}

func TestIntervalSchedulerRunsCallbacksSynchronously(t *testing.T) {
	t.Parallel()
	scheduler := NewIntervalScheduler()
	defer scheduler.Stop()

	const interval = time.Millisecond
	var active atomic.Int32
	var calls atomic.Int32
	var overlapped atomic.Bool
	var firstStarted sync.Once
	started := make(chan struct{})

	scheduler.AddCallback(interval, func(start time.Time, end time.Time) {
		if active.Add(1) > 1 {
			overlapped.Store(true)
		}
		if calls.Add(1) == 1 {
			firstStarted.Do(func() {
				close(started)
			})
			time.Sleep(3 * interval)
		}
		active.Add(-1)
	})

	select {
	case <-started:
	case <-time.After(10 * time.Millisecond):
		t.Fatal("timed out waiting for first callback")
	}

	time.Sleep(5 * interval)
	if overlapped.Load() {
		t.Fatal("callback executions overlapped")
	}
	if calls.Load() < 2 {
		t.Fatal("scheduler did not continue after a long callback")
	}
}
