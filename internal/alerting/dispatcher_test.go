package alerting

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/config"
)

func TestDispatcherRecoversFromPanicAndContinues(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	secondCall := make(chan struct{})
	dispatcher := newDispatcher(config.NewFakeConfig(), 2, func(context.Context, config.Config, AlertingOptions) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			panic("broken alert sender")
		}
		close(secondCall)
		return nil
	})
	t.Cleanup(dispatcher.Stop)

	require.NoError(t, dispatcher.Submit(AlertingOptions{AlertName: "first"}))
	require.NoError(t, dispatcher.Submit(AlertingOptions{AlertName: "second"}))

	select {
	case <-secondCall:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not continue after panic")
	}
}

func TestDispatcherBoundsQueueAndStopsActiveDelivery(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	dispatcher := newDispatcher(config.NewFakeConfig(), 1, func(ctx context.Context, _ config.Config, _ AlertingOptions) error {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return ctx.Err()
	})

	require.NoError(t, dispatcher.Submit(AlertingOptions{AlertName: "first"}))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("alert delivery did not start")
	}
	require.NoError(t, dispatcher.Submit(AlertingOptions{AlertName: "second"}))
	require.ErrorIs(t, dispatcher.Submit(AlertingOptions{AlertName: "third"}), ErrQueueFull)

	stopped := make(chan struct{})
	go func() {
		dispatcher.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not stop active delivery")
	}
	require.ErrorIs(t, dispatcher.Submit(AlertingOptions{AlertName: "after-stop"}), ErrDispatcherStopped)
}
