package collection

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func TestCollectionFailureIdentifiesThePrometheusQuery(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"status":"success","data":{"result":[]}}`)),
		}, nil
	})}
	collector := &collector{client: client, chunkStore: ecdf.NewFakeChunkStore()}
	service := &config.Service{
		Id: 1, Name: "checkout", PrometheusURL: "http://prometheus.example",
		LoadQuery: "sum(rate(http_requests_total[5m]))", LatencyQuery: "histogram_quantile(0.99, latency_bucket)", Interval: time.Minute,
	}

	err := collector.collectSamples(context.Background(), service, time.Unix(0, 0), time.Unix(60, 0))

	require.EqualError(t, err, `Prometheus load query "sum(rate(http_requests_total[5m]))" failed: no data returned`)
}

func TestCollectionFailureIdentifiesTheLatencyQuery(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		body := `{"status":"success","data":{"result":[{"values":[[60,"1"]]}]}}`
		if requests == 2 {
			body = `{"status":"success","data":{"result":[]}}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	collector := &collector{client: client, chunkStore: ecdf.NewFakeChunkStore()}
	service := &config.Service{
		Id: 1, Name: "checkout", PrometheusURL: "http://prometheus.example",
		LoadQuery: "sum(rate(http_requests_total[5m]))", LatencyQuery: "histogram_quantile(0.99, latency_bucket)", Interval: time.Minute,
	}

	err := collector.collectSamples(context.Background(), service, time.Unix(0, 0), time.Unix(60, 0))

	require.EqualError(t, err, `Prometheus latency query "histogram_quantile(0.99, latency_bucket)" failed: no data returned`)
}

func TestHistoricalImportBatchesAYearBelowPrometheusPointLimit(t *testing.T) {
	const maxPoints = 10_000
	var (
		requestsMu sync.Mutex
		requests   int
	)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		start, err := strconv.ParseInt(request.URL.Query().Get("start"), 10, 64)
		require.NoError(t, err)
		end, err := strconv.ParseInt(request.URL.Query().Get("end"), 10, 64)
		require.NoError(t, err)
		points := ((end - start) / 15) + 1
		require.LessOrEqual(t, points, int64(maxPoints))
		requestsMu.Lock()
		requests++
		requestsMu.Unlock()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(fmt.Sprintf(
				`{"status":"success","data":{"result":[{"values":[[%d,"1"]]}]}}`,
				end,
			))),
		}, nil
	})}
	collector := &collector{
		client:     client,
		chunkStore: ecdf.NewFakeChunkStore(),
	}
	service := &config.Service{
		Id:            1,
		Name:          "checkout",
		PrometheusURL: "http://prometheus.example",
		LoadQuery:     "load",
		LatencyQuery:  "latency",
		Interval:      time.Hour,
	}
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	_, err := collector.Import(context.Background(), service, start, start.Add(365*24*time.Hour), nil)

	require.NoError(t, err)
	requestsMu.Lock()
	defer requestsMu.Unlock()
	require.Greater(t, requests, 2)
}

func TestHistoricalImportContinuesAfterBatchWithNoData(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var requests int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		body := `{"status":"success","data":{"result":[]}}`
		if request.URL.Query().Get("start") != strconv.FormatInt(start.Unix(), 10) {
			batchStart, err := strconv.ParseInt(request.URL.Query().Get("start"), 10, 64)
			require.NoError(t, err)
			body = fmt.Sprintf(`{"status":"success","data":{"result":[{"values":[[%d,"1"]]}]}}`, batchStart+15)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	collector := &collector{client: client, chunkStore: ecdf.NewFakeChunkStore()}
	service := &config.Service{
		Id: 1, Name: "checkout", PrometheusURL: "http://prometheus.example",
		LoadQuery: "load", LatencyQuery: "latency", Interval: time.Hour,
	}

	summary, err := collector.Import(context.Background(), service, start, start.Add(historicalBatchSpan+time.Hour), nil)

	require.NoError(t, err)
	require.Greater(t, requests, 1)
	require.Equal(t, ImportSummary{TotalWindows: 43, ImportedWindows: 1, GapWindows: 42}, summary)
}

type importRecordingChunkStore struct {
	ecdf.ChunkStore
	timestamps map[int][]time.Time
}

type backpressureAnalysisQueue struct {
	contextSubmissions int
}

func (*backpressureAnalysisQueue) Submit(AnalysisRequest) error {
	return ErrAnalysisQueueFull
}

func (q *backpressureAnalysisQueue) SubmitContext(_ context.Context, _ AnalysisRequest) error {
	q.contextSubmissions++
	return nil
}

func (s *importRecordingChunkStore) WriteChunk(serviceID, indicatorID int, generation int64, timestamp time.Time, chunk []byte) error {
	s.timestamps[indicatorID] = append(s.timestamps[indicatorID], timestamp)
	return s.ChunkStore.WriteChunk(serviceID, indicatorID, generation, timestamp, chunk)
}

func TestHistoricalImportWritesOneTimeChunkPerServiceInterval(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	values := make([]string, 0, 8)
	for timestamp := start; timestamp.Before(start.Add(2 * time.Hour)); timestamp = timestamp.Add(15 * time.Minute) {
		values = append(values, fmt.Sprintf(`[%d,"1"]`, timestamp.Unix()))
	}
	body := fmt.Sprintf(`{"status":"success","data":{"result":[{"values":[%s]}]}}`, strings.Join(values, ","))
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	chunks := &importRecordingChunkStore{ChunkStore: ecdf.NewFakeChunkStore(), timestamps: make(map[int][]time.Time)}
	collector := &collector{client: client, chunkStore: chunks}
	service := &config.Service{
		Id: 1, Name: "checkout", PrometheusURL: "http://prometheus.example",
		LoadQuery: "load", LatencyQuery: "latency", Interval: time.Hour,
	}

	_, err := collector.Import(context.Background(), service, start, start.Add(2*time.Hour), nil)

	require.NoError(t, err)
	require.Equal(t, []time.Time{start.Add(time.Hour), start.Add(2 * time.Hour)}, chunks.timestamps[LoadLatencyIndicator])
	require.Len(t, chunks.timestamps[TimeOfDayIndicator], 8)
}

func TestHistoricalImportUsesAnalysisBackpressureInsteadOfDroppingChunks(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	body := fmt.Sprintf(
		`{"status":"success","data":{"result":[{"values":[[%d,"1"]]}]}}`,
		start.Unix(),
	)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	analysis := &backpressureAnalysisQueue{}
	collector := &collector{
		client: client, chunkStore: ecdf.NewFakeChunkStore(), analyzer: analysis,
	}
	service := &config.Service{
		Id: 1, Name: "checkout", PrometheusURL: "http://prometheus.example",
		LoadQuery: "load", LatencyQuery: "latency", Interval: time.Hour,
	}

	_, err := collector.Import(context.Background(), service, start, start.Add(time.Hour), nil)

	require.NoError(t, err)
	require.Equal(t, 2, analysis.contextSubmissions)
}

type pendingRecovery struct {
	resolved chan int
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func (*pendingRecovery) Stop() {}

func (*pendingRecovery) Register(*config.Service, historicalCollector) {}

func (*pendingRecovery) Unregister(int) {}

func (*pendingRecovery) EnqueueFailure(context.Context, *config.Service, time.Time, time.Time, error) error {
	return nil
}

func (r *pendingRecovery) ResolveCollection(_ context.Context, serviceID int, _ time.Time) error {
	select {
	case r.resolved <- serviceID:
	default:
	}
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
	recovery := &pendingRecovery{resolved: make(chan int, 1)}
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
				select {
				case serviceID := <-recovery.resolved:
					require.Equal(t, service.Id, serviceID)
				case <-time.After(time.Second):
					t.Fatal("collection alert was not resolved after live collection succeeded")
				}
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

func TestFailedScheduledCollectionDoesNotResolveCollectionAlert(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("prometheus unavailable")
	})}
	clock := NewFakeClock(time.Date(2026, 7, 27, 12, 0, 30, 0, time.UTC))
	scheduler := newTestScheduler(clock)
	recovery := &pendingRecovery{resolved: make(chan int, 1)}
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

	select {
	case event := <-events:
		require.Equal(t, "tracking_started", event.Kind)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tracking event")
	}
	select {
	case event := <-events:
		require.Equal(t, "collection_failed", event.Kind)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for collection failure")
	}
	select {
	case serviceID := <-recovery.resolved:
		t.Fatalf("collection alert resolved for failed service %d", serviceID)
	default:
	}
}
