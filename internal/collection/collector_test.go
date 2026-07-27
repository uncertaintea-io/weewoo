package collection

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

type unavailableJointStore struct{}

func (unavailableJointStore) Publish(context.Context, int, int, time.Time, func(io.Writer) error) (int64, bool, error) {
	return 0, false, errors.New("unavailable")
}

func (unavailableJointStore) ReadCurrent(context.Context, int, int) ([]byte, error) {
	return nil, errors.New("unavailable")
}

func TestCollectionSucceedsWhenAnalysisFails(t *testing.T) {
	prometheus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status":"success",
			"data":{"result":[{"values":[[1710000000,"1"]]}]}
		}`))
	}))
	t.Cleanup(prometheus.Close)

	service := &config.Service{
		Id:            1,
		Name:          "checkout",
		PrometheusURL: prometheus.URL,
		LoadQuery:     "load",
		LatencyQuery:  "latency",
		Interval:      time.Minute,
	}
	analysisWorker := NewAnalysisWorker(config.NewFakeConfig(), unavailableJointStore{}, nil, nil, 1)
	t.Cleanup(analysisWorker.Stop)
	collector := &collector{
		client:     prometheus.Client(),
		chunkStore: ecdf.NewFakeChunkStore(),
		analyzer:   analysisWorker,
	}

	err := collector.collectSamples(context.Background(), service, time.Unix(1_710_000_000, 0), time.Unix(1_710_000_060, 0))

	require.NoError(t, err)
}

type pendingRecovery struct {
	deferred chan struct{}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func (*pendingRecovery) Stop() {}

func (*pendingRecovery) Register(*config.Service, historicalCollector) {}

func (*pendingRecovery) Unregister(int) {}

func (*pendingRecovery) HasPending(context.Context, int) (bool, error) {
	return true, nil
}

func (r *pendingRecovery) EnqueueDeferred(context.Context, *config.Service, time.Time, time.Time) error {
	select {
	case r.deferred <- struct{}{}:
	default:
	}
	return nil
}

func (*pendingRecovery) EnqueueFailure(context.Context, *config.Service, time.Time, time.Time, error) error {
	return nil
}

func TestScheduledCollectionContinuesWhileRecoveryIsPending(t *testing.T) {
	var (
		requestsMu sync.Mutex
		requests   int
	)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requestsMu.Lock()
		requests++
		requestsMu.Unlock()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"status":"success",
				"data":{"result":[{"values":[[1710000000,"1"]]}]}
			}`)),
		}, nil
	})}

	clock := NewFakeClock(time.Date(2026, 7, 27, 12, 0, 30, 0, time.UTC))
	scheduler := newTestScheduler(clock)
	recovery := &pendingRecovery{deferred: make(chan struct{}, 1)}
	events := make(chan CollectorEvent, 4)
	collector := &collector{
		client:     client,
		chunkStore: ecdf.NewFakeChunkStore(),
		scheduler:  scheduler,
		recovery:   recovery,
		events: func(event CollectorEvent) {
			events <- event
		},
	}
	t.Cleanup(collector.Stop)
	service := &config.Service{
		Id:            1,
		Name:          "checkout",
		PrometheusURL: "http://prometheus.example",
		LoadQuery:     "load",
		LatencyQuery:  "latency",
		Interval:      time.Minute,
	}

	require.NoError(t, collector.Schedule(service))
	clock.Advance(30 * time.Second)

	deadline := time.After(time.Second)
	for {
		select {
		case event := <-events:
			if event.Kind == "collection_succeeded" {
				requestsMu.Lock()
				defer requestsMu.Unlock()
				require.Equal(t, 2, requests)
				return
			}
			if event.Kind == "collection_delayed" {
				t.Fatal("live collection was delayed by pending recovery")
			}
		case <-deadline:
			t.Fatal("timed out waiting for live collection")
		}
	}
}
