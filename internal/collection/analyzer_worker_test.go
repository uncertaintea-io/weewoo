package collection

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

type panicThenFailJointStore struct {
	mu         sync.Mutex
	calls      int
	secondCall chan struct{}
	secondOnce sync.Once
}

func (s *panicThenFailJointStore) Publish(context.Context, int, int, time.Time, func(io.Writer) error) (int64, bool, error) {
	return 0, false, errors.New("unexpected publish")
}

func (s *panicThenFailJointStore) ReadCurrent(context.Context, int, int) ([]byte, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if call == 1 {
		panic("broken analyzer dependency")
	}
	s.secondOnce.Do(func() { close(s.secondCall) })
	return nil, errors.New("unavailable")
}

func validAnalysisRequest() AnalysisRequest {
	return AnalysisRequest{
		Service:     config.Service{Id: 1, Name: "checkout"},
		IndicatorID: LoadLatencyIndicator,
		Timestamp:   time.Unix(1_710_000_060, 0),
		Loads:       []ecdf.Sample{{Value: 1, Count: 1}},
		Latencies:   []ecdf.Sample{{Value: 1, Count: 1}},
	}
}

func TestAnalysisWorkerRecoversFromPanicAndContinues(t *testing.T) {
	store := &panicThenFailJointStore{secondCall: make(chan struct{})}
	worker := NewAnalysisWorker(config.NewFakeConfig(), store, nil, nil, 2)
	t.Cleanup(worker.Stop)

	require.NoError(t, worker.Submit(validAnalysisRequest()))
	require.NoError(t, worker.Submit(validAnalysisRequest()))

	select {
	case <-store.secondCall:
	case <-time.After(time.Second):
		t.Fatal("analysis worker did not continue after panic")
	}
}

type blockingJointStore struct {
	started chan struct{}
	once    sync.Once
}

func (s *blockingJointStore) Publish(context.Context, int, int, time.Time, func(io.Writer) error) (int64, bool, error) {
	return 0, false, errors.New("unexpected publish")
}

func (s *blockingJointStore) ReadCurrent(ctx context.Context, _ int, _ int) ([]byte, error) {
	s.once.Do(func() { close(s.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestAnalysisWorkerBoundsQueueAndStopsActiveWork(t *testing.T) {
	store := &blockingJointStore{started: make(chan struct{})}
	worker := NewAnalysisWorker(config.NewFakeConfig(), store, nil, nil, 1)

	require.NoError(t, worker.Submit(validAnalysisRequest()))
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("analysis did not start")
	}
	require.NoError(t, worker.Submit(validAnalysisRequest()))
	require.ErrorIs(t, worker.Submit(validAnalysisRequest()), ErrAnalysisQueueFull)

	stopped := make(chan struct{})
	go func() {
		worker.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("analysis worker did not stop active work")
	}
	require.ErrorIs(t, worker.Submit(validAnalysisRequest()), ErrAnalyzerStopped)
}

type failOnceVerdictStore struct {
	mu        sync.Mutex
	calls     int
	persisted chan struct{}
}

func (s *failOnceVerdictStore) WriteVerdict(context.Context, int, int, time.Time, bool, float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls == 1 {
		return errors.New("temporary database failure")
	}
	close(s.persisted)
	return nil
}

func (s *failOnceVerdictStore) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestAnalysisWorkerRetriesFailedVerdictWrite(t *testing.T) {
	setFakeJECDF(t, `#!/bin/sh
if [ "$1" != "query" ]; then
exit 2
fi
cat >/dev/null
printf '\002\000\000\000\000\000\000\000\000\000\000\000\000\000\000\000\000\077\360\000\000\000\000\000\000\077\360\000\000\000\000\000\000'
`)
	verdicts := &failOnceVerdictStore{persisted: make(chan struct{})}
	worker := NewAnalysisWorker(config.NewFakeConfig(), staticJointStore{}, verdicts, nil, 1)
	t.Cleanup(worker.Stop)

	require.NoError(t, worker.Submit(validAnalysisRequest()))

	select {
	case <-verdicts.persisted:
	case <-time.After(time.Second):
		t.Fatal("analysis worker did not retry the failed verdict write")
	}
	require.Equal(t, 2, verdicts.Calls())
}

type failingVerdictStore struct {
	mu       sync.Mutex
	calls    int
	finished chan struct{}
}

func (s *failingVerdictStore) WriteVerdict(context.Context, int, int, time.Time, bool, float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls == verdictMaxAttempts {
		close(s.finished)
	}
	return errors.New("persistent database failure")
}

func (s *failingVerdictStore) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestAnalysisWorkerStopsRetryingVerdictAfterMaximumAttempts(t *testing.T) {
	setFakeJECDF(t, `#!/bin/sh
if [ "$1" != "query" ]; then
exit 2
fi
cat >/dev/null
printf '\002\000\000\000\000\000\000\000\000\000\000\000\000\000\000\000\000\077\360\000\000\000\000\000\000\077\360\000\000\000\000\000\000'
`)
	verdicts := &failingVerdictStore{finished: make(chan struct{})}
	worker := NewAnalysisWorker(config.NewFakeConfig(), staticJointStore{}, verdicts, nil, 1)
	t.Cleanup(worker.Stop)

	require.NoError(t, worker.Submit(validAnalysisRequest()))

	select {
	case <-verdicts.finished:
	case <-time.After(time.Second):
		t.Fatal("analysis worker did not complete the configured verdict attempts")
	}
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, verdictMaxAttempts, verdicts.Calls())
}
