package collection

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

func (s *panicThenFailJointStore) ReadCurrent(context.Context, int, int) ([]byte, string, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if call == 1 {
		panic("broken analyzer dependency")
	}
	s.secondOnce.Do(func() { close(s.secondCall) })
	return nil, "", errors.New("unavailable")
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

func TestAnalysisWorkerDoesNotMixTimeOfDayObservationsAcrossGenerations(t *testing.T) {
	cfg := config.NewFakeConfig()
	service := &config.Service{Id: 7, Generation: 2, Interval: time.Minute}
	require.NoError(t, cfg.WriteService(service))
	worker := &AnalysisWorker{
		cfg: cfg, ctx: context.Background(), observations: make(map[serviceGeneration][]Observation),
	}
	now := time.Now().UTC()

	worker.analyze(AnalysisRequest{
		Service:     config.Service{Id: service.Id, Generation: 1, Interval: time.Minute},
		IndicatorID: TimeOfDayIndicator, Timestamp: now,
		Observations:    []Observation{{Timestamp: now, Value: 1}},
		ChunkTimestamps: []time.Time{now},
	})
	worker.analyze(AnalysisRequest{
		Service: *service, IndicatorID: TimeOfDayIndicator, Timestamp: now,
		Observations:    []Observation{{Timestamp: now, Value: 2}},
		ChunkTimestamps: []time.Time{now},
	})

	key := serviceGeneration{serviceID: service.Id, generation: service.Generation}
	require.Len(t, worker.observations[key], 1)
	assert.Equal(t, 2.0, worker.observations[key][0].Value)
	assert.NotContains(t, worker.observations, serviceGeneration{serviceID: service.Id, generation: 1})
}

type orderedJointStore struct {
	calls   chan int
	release chan struct{}
}

func (*orderedJointStore) Publish(context.Context, int, int, time.Time, func(io.Writer) error) (int64, bool, error) {
	return 0, false, errors.New("unexpected publish")
}

func (s *orderedJointStore) ReadCurrent(ctx context.Context, serviceID, _ int) ([]byte, string, error) {
	select {
	case s.calls <- serviceID:
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	if serviceID == 1 {
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
	return nil, "", errors.New("analysis probe complete")
}

func TestAnalysisWorkerPrioritizesLiveWorkOverHistoricalBacklog(t *testing.T) {
	store := &orderedJointStore{calls: make(chan int, 3), release: make(chan struct{})}
	worker := NewAnalysisWorker(config.NewFakeConfig(), store, nil, nil, 2)
	t.Cleanup(worker.Stop)

	firstHistorical := validAnalysisRequest()
	firstHistorical.Service.Id = 1
	firstHistorical.Historical = true
	require.NoError(t, worker.Submit(firstHistorical))
	require.Equal(t, 1, <-store.calls)

	secondHistorical := validAnalysisRequest()
	secondHistorical.Service.Id = 2
	secondHistorical.Historical = true
	require.NoError(t, worker.Submit(secondHistorical))
	live := validAnalysisRequest()
	live.Service.Id = 3
	require.NoError(t, worker.Submit(live))
	close(store.release)

	require.Equal(t, 3, <-store.calls)
	require.Equal(t, 2, <-store.calls)
}

func TestAnalysisWorkerPreservesHistoricalFlagOnFailure(t *testing.T) {
	alerts := &recordingAlertQueue{}
	worker := NewAnalysisWorker(config.NewFakeConfig(), unavailableJointStore{}, nil, alerts, 1)
	t.Cleanup(worker.Stop)
	request := validAnalysisRequest()
	request.Historical = true

	require.NoError(t, worker.Submit(request))

	require.Eventually(t, func() bool {
		alerts.mu.Lock()
		defer alerts.mu.Unlock()
		return len(alerts.failures) == 1
	}, time.Second, time.Millisecond)
	alerts.mu.Lock()
	defer alerts.mu.Unlock()
	require.True(t, alerts.failures[0].Historical)
}

type blockingJointStore struct {
	started chan struct{}
	once    sync.Once
}

func (s *blockingJointStore) Publish(context.Context, int, int, time.Time, func(io.Writer) error) (int64, bool, error) {
	return 0, false, errors.New("unexpected publish")
}

func (s *blockingJointStore) ReadCurrent(ctx context.Context, _ int, _ int) ([]byte, string, error) {
	s.once.Do(func() { close(s.started) })
	<-ctx.Done()
	return nil, "", ctx.Err()
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

type failOnceChunkStore struct {
	ecdf.ChunkStore
	mu        sync.Mutex
	calls     int
	persisted chan struct{}
}

func (s *failOnceChunkStore) WriteVerdict(context.Context, int, int, int64, time.Time, bool, float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls == 1 {
		return errors.New("temporary database failure")
	}
	close(s.persisted)
	return nil
}

func (s *failOnceChunkStore) Calls() int {
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
	verdicts := &failOnceChunkStore{persisted: make(chan struct{})}
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

type failingChunkStore struct {
	ecdf.ChunkStore
	mu       sync.Mutex
	calls    int
	finished chan struct{}
}

func (s *failingChunkStore) WriteVerdict(context.Context, int, int, int64, time.Time, bool, float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls == verdictMaxAttempts {
		close(s.finished)
	}
	return errors.New("persistent database failure")
}

func (s *failingChunkStore) Calls() int {
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
	verdicts := &failingChunkStore{finished: make(chan struct{})}
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
