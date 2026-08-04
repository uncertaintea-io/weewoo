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
			"data":{"resultType":"matrix","result":[{"values":[[1710000060,"1"]]}]}
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
			Body:       io.NopCloser(strings.NewReader(`{"status":"success","data":{"resultType":"matrix","result":[]}}`)),
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
		body := `{"status":"success","data":{"resultType":"matrix","result":[{"values":[[60,"1"]]}]}}`
		if requests == 2 {
			body = `{"status":"success","data":{"resultType":"matrix","result":[]}}`
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

func TestHistoricalImportQueriesEachTimeChunk(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	var requested []collectionWindow
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		windowStart := prometheusRequestTime(t, request, "start")
		windowEnd := prometheusRequestTime(t, request, "end")
		requested = append(requested, collectionWindow{Start: windowStart, End: windowEnd})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(fmt.Sprintf(
				`{"status":"success","data":{"resultType":"matrix","result":[{"values":[[%d,"1"]]}]}}`,
				windowEnd.Unix(),
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

	summary, err := collector.Import(context.Background(), service, start, start.Add(3*time.Hour), nil)

	require.NoError(t, err)
	require.Equal(t, ImportSummary{TotalWindows: 3, ImportedWindows: 3}, summary)
	require.Equal(t, []collectionWindow{
		{Start: start, End: start.Add(time.Hour)},
		{Start: start, End: start.Add(time.Hour)},
		{Start: start.Add(time.Hour), End: start.Add(2 * time.Hour)},
		{Start: start.Add(time.Hour), End: start.Add(2 * time.Hour)},
		{Start: start.Add(2 * time.Hour), End: start.Add(3 * time.Hour)},
		{Start: start.Add(2 * time.Hour), End: start.Add(3 * time.Hour)},
	}, requested)
}

func TestHistoricalImportContinuesAfterWindowWithNoData(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var requests int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		body := `{"status":"success","data":{"resultType":"matrix","result":[]}}`
		if prometheusRequestTime(t, request, "start") != start {
			windowEnd := prometheusRequestTime(t, request, "end")
			body = fmt.Sprintf(`{"status":"success","data":{"resultType":"matrix","result":[{"values":[[%d,"1"]]}]}}`, windowEnd.Unix())
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

	summary, err := collector.Import(context.Background(), service, start, start.Add(2*time.Hour), nil)

	require.NoError(t, err)
	require.Equal(t, 3, requests)
	require.Equal(t, ImportSummary{TotalWindows: 2, ImportedWindows: 1, GapWindows: 1}, summary)
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
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		windowEnd := prometheusRequestTime(t, request, "end")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(fmt.Sprintf(
				`{"status":"success","data":{"resultType":"matrix","result":[{"values":[[%d,"1"]]}]}}`, windowEnd.Unix(),
			))),
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
	require.Equal(t, []time.Time{start.Add(time.Hour), start.Add(2 * time.Hour)}, chunks.timestamps[TimeOfDayIndicator])
}

func TestHistoricalImportWritesHistogramIncreaseWithoutBucketTimestamps(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		require.NoError(t, request.ParseForm())
		var body string
		switch request.Form.Get("query") {
		case "load":
			body = fmt.Sprintf(
				`{"status":"success","data":{"resultType":"matrix","result":[{"values":[[%d,"1"]]}]}}`,
				end.Unix(),
			)
		case "latency":
			body = fmt.Sprintf(
				`{"status":"success","data":{"resultType":"matrix","result":[{"histograms":[[%d,{"count":"10","sum":"5","buckets":[[0,"0","1","10"]]}],[%d,{"count":"13","sum":"8","buckets":[[0,"0","1","13"]]}]]}]}}`,
				start.Unix(), end.Unix(),
			)
		default:
			t.Fatalf("unexpected Prometheus query %q", request.Form.Get("query"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	chunks := ecdf.NewFakeChunkStore()
	collector := &collector{client: client, chunkStore: chunks}
	service := &config.Service{
		Id: 1, Name: "checkout", PrometheusURL: "http://prometheus.example",
		LoadQuery: "load", LatencyQuery: "latency", Interval: time.Hour,
	}

	summary, err := collector.Import(context.Background(), service, start, end, nil)

	require.NoError(t, err)
	require.Equal(t, ImportSummary{TotalWindows: 1, ImportedWindows: 1}, summary)
	chunk, err := chunks.ReadChunk(service.Id, LoadLatencyIndicator, end)
	require.NoError(t, err)
	_, _, latencies, err := ecdf.Decode(chunk)
	require.NoError(t, err)
	require.Equal(t, []ecdf.Sample{{Value: 1, Count: 3}}, latencies)
}

func TestHistoricalImportUsesPartialFinalWindowAsPrometheusStep(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(90 * time.Minute)
	var steps []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		require.NoError(t, request.ParseForm())
		steps = append(steps, request.Form.Get("step"))
		windowEnd := prometheusRequestTime(t, request, "end")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(fmt.Sprintf(
				`{"status":"success","data":{"resultType":"matrix","result":[{"values":[[%d,"1"]]}]}}`, windowEnd.Unix(),
			))),
		}, nil
	})}
	collector := &collector{client: client, chunkStore: ecdf.NewFakeChunkStore()}
	service := &config.Service{
		Id: 1, Name: "checkout", PrometheusURL: "http://prometheus.example",
		LoadQuery: "load", LatencyQuery: "latency", Interval: time.Hour,
	}

	summary, err := collector.Import(context.Background(), service, start, end, nil)

	require.NoError(t, err)
	require.Equal(t, ImportSummary{TotalWindows: 2, ImportedWindows: 2}, summary)
	require.Equal(t, []string{"3600", "3600", "1800", "1800"}, steps)
}

func TestHistoricalImportUsesAnalysisBackpressureInsteadOfDroppingChunks(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		windowEnd := prometheusRequestTime(t, request, "end")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(fmt.Sprintf(
				`{"status":"success","data":{"resultType":"matrix","result":[{"values":[[%d,"1"]]}]}}`, windowEnd.Unix(),
			))),
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

func prometheusRequestTime(t *testing.T, request *http.Request, parameter string) time.Time {
	t.Helper()
	require.NoError(t, request.ParseForm())
	seconds, err := strconv.ParseFloat(request.Form.Get(parameter), 64)
	require.NoError(t, err)
	return time.Unix(int64(seconds), 0).UTC()
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
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestsMu.Lock()
		requests++
		requestsMu.Unlock()
		windowEnd := prometheusRequestTime(t, request, "end")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{
				"status":"success",
				"data":{"resultType":"matrix","result":[{"values":[[%d,"1"]]}]}
			}`, windowEnd.Unix()))),
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
