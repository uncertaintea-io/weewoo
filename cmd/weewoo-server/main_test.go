package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/collection"
	"github.com/uncertaintea-io/weewoo/internal/config"
)

type fakeServiceCollector struct {
	scheduled   *config.Service
	unscheduled int
	imported    *config.Service
	start       time.Time
	end         time.Time
}

func (c *fakeServiceCollector) Stop() {}
func (c *fakeServiceCollector) Unschedule(serviceID int) {
	c.unscheduled = serviceID
}
func (c *fakeServiceCollector) Schedule(service *config.Service) error {
	c.scheduled = service
	return nil
}

func TestLiveServiceTrackerSchedulesCollectionAndPublishing(t *testing.T) {
	collector := &fakeServiceCollector{}
	builderServiceID := 0
	tracker := &liveServiceTracker{
		collector: collector,
		scheduleBuilder: func(serviceID int) error {
			builderServiceID = serviceID
			return nil
		},
	}
	service := &config.Service{Id: 42, Name: "checkout", Interval: time.Minute}

	require.NoError(t, tracker.Schedule(service))

	assert.Same(t, service, collector.scheduled)
	assert.Equal(t, 42, builderServiceID)
}

func TestLiveServiceTrackerUnschedulesCollectionAndPublishing(t *testing.T) {
	removed := make(chan int, 1)
	scheduler := collection.NewIntervalScheduler(collection.WithSchedulerEventHandler(func(event collection.SchedulerEvent) {
		if event.Kind == collection.SchedulerEventCallbackRemoved {
			removed <- event.ID
		}
	}))
	defer scheduler.Stop()
	callback := func(context.Context, time.Time, time.Time) collection.IntervalResult {
		return collection.IntervalSuccess()
	}
	serviceID := 1003
	require.NoError(t, scheduler.AddCallback(collection.CallbackID(serviceID, collection.CollectCallback), time.Hour, callback))
	require.NoError(t, scheduler.AddCallback(collection.CallbackID(serviceID, collection.BuilderCallback), time.Hour, callback))

	collector := &fakeServiceCollector{}
	tracker := &liveServiceTracker{collector: collector, scheduler: scheduler}
	tracker.Unschedule(serviceID)

	assert.Equal(t, serviceID, collector.unscheduled)
	select {
	case removedID := <-removed:
		assert.Equal(t, collection.CallbackID(serviceID, collection.BuilderCallback), removedID)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for builder callback removal")
	}
}

func TestAppServerWriteTimeoutExceedsPrometheusTestTimeout(t *testing.T) {
	assert.Greater(t, appServerWriteTimeout, prometheusTestTimeout,
		"the server must leave time for the Prometheus test handler to write its response")
}

func (c *fakeServiceCollector) Import(_ context.Context, service *config.Service, start, end time.Time) error {
	c.imported, c.start, c.end = service, start, end
	return nil
}

func TestNewListAllServicesHandler(t *testing.T) {
	cfg := config.NewFakeConfig()
	require.NoError(t, cfg.WriteService(&config.Service{
		Name:          "checkout",
		PrometheusURL: "http://prometheus.example.com",
		LoadQuery:     "sum(rate(http_requests_total[5m]))",
		LatencyQuery:  "histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m]))",
		Interval:      30 * time.Second,
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/services", nil)
	recorder := httptest.NewRecorder()

	NewListAllServicesHandler(cfg).ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))

	var services []serviceResponse
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&services))
	require.Len(t, services, 1)
	assert.Equal(t, serviceResponse{
		ID:              1,
		Name:            "checkout",
		PrometheusURL:   "http://prometheus.example.com",
		LoadQuery:       "sum(rate(http_requests_total[5m]))",
		LatencyQuery:    "histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m]))",
		IntervalSeconds: 30,
		Tracking:        trackingStatus{State: "pending", Activity: []activityEntry{}},
		Imports:         []importJob{},
	}, services[0])
}

func TestNewListAllServicesHandlerRejectsNonGetMethods(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/services", nil)
	recorder := httptest.NewRecorder()

	NewListAllServicesHandler(config.NewFakeConfig()).ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusMethodNotAllowed, recorder.Code)
	assert.Equal(t, http.MethodGet, recorder.Header().Get("Allow"))
}

func TestAppRoutesListRequestsThroughLiveServiceAPI(t *testing.T) {
	cfg := config.NewFakeConfig()
	service := &config.Service{
		Name:          "checkout",
		PrometheusURL: "http://prometheus.example.com",
		LoadQuery:     "load",
		LatencyQuery:  "latency",
		Interval:      time.Minute,
	}
	require.NoError(t, cfg.WriteService(service))
	monitor := newTrackingMonitor()
	monitor.record(service.Id, "healthy", "collection_succeeded", "collected", time.Now().UTC())
	tracker := &fakeServiceCollector{}
	apiHandler := NewServiceAPIHandler(cfg, tracker, monitor, newImportManager(tracker, monitor), http.DefaultClient)
	appHandler := http.NewServeMux()
	registerAPIHandlers(appHandler, http.NotFoundHandler(), apiHandler)
	recorder := httptest.NewRecorder()
	appHandler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/services", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	var response []serviceResponse
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
	require.Len(t, response, 1)
	assert.Equal(t, "healthy", response[0].Tracking.State)
}

func TestNewCreateServiceHandlerCreatesSchedulesAndImportsService(t *testing.T) {
	cfg := config.NewFakeConfig()
	collector := &fakeServiceCollector{}
	body := []byte(`{
		"name":"checkout",
		"prometheusUrl":"http://prometheus.example.com",
		"loadQuery":"load",
		"latencyQuery":"latency",
		"intervalSeconds":30,
		"importStart":"2026-07-01T00:00:00Z",
		"importEnd":"2026-07-02T00:00:00Z"
	}`)
	request := httptest.NewRequest(http.MethodPost, "/api/services", bytes.NewReader(body))
	recorder := httptest.NewRecorder()

	NewCreateServiceHandler(cfg, collector).ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	assert.Equal(t, "/api/services/1", recorder.Header().Get("Location"))
	service, err := cfg.ReadService(1)
	require.NoError(t, err)
	assert.Equal(t, "checkout", service.Name)
	assert.Same(t, service, collector.scheduled)
	assert.Same(t, service, collector.imported)
	assert.Equal(t, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), collector.start)
	assert.Equal(t, time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC), collector.end)
}

func TestNewCreateServiceHandlerValidatesInput(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/services", bytes.NewBufferString(`{
		"name":"", "prometheusUrl":"javascript:alert(1)", "intervalSeconds":0
	}`))
	recorder := httptest.NewRecorder()

	NewCreateServiceHandler(config.NewFakeConfig(), &fakeServiceCollector{}).ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestServiceAPIReportsTrackingStatusAndDeletesService(t *testing.T) {
	cfg := config.NewFakeConfig()
	service := &config.Service{Name: "checkout", PrometheusURL: "http://prometheus.example.com", LoadQuery: "load", LatencyQuery: "latency", Interval: time.Minute}
	require.NoError(t, cfg.WriteService(service))
	monitor := newTrackingMonitor()
	now := time.Now().UTC()
	monitor.record(service.Id, "healthy", "collection_succeeded", "collected", now)
	tracker := &fakeServiceCollector{}
	imports := newImportManager(tracker, monitor)
	handler := NewServiceAPIHandler(cfg, tracker, monitor, imports, http.DefaultClient)

	getRecorder := httptest.NewRecorder()
	handler.ServeHTTP(getRecorder, httptest.NewRequest(http.MethodGet, "/api/services/1", nil))
	require.Equal(t, http.StatusOK, getRecorder.Code)
	var response serviceResponse
	require.NoError(t, json.NewDecoder(getRecorder.Body).Decode(&response))
	assert.Equal(t, "healthy", response.Tracking.State)
	assert.Equal(t, "collected", response.Tracking.Activity[0].Message)

	deleteRecorder := httptest.NewRecorder()
	handler.ServeHTTP(deleteRecorder, httptest.NewRequest(http.MethodDelete, "/api/services/1", nil))
	assert.Equal(t, http.StatusNoContent, deleteRecorder.Code)
	_, err := cfg.ReadService(1)
	assert.Error(t, err)
}

func TestServiceAPICreatesBackgroundImport(t *testing.T) {
	cfg := config.NewFakeConfig()
	monitor := newTrackingMonitor()
	tracker := &fakeServiceCollector{}
	imports := newImportManager(tracker, monitor)
	handler := NewServiceAPIHandler(cfg, tracker, monitor, imports, http.DefaultClient)
	body := bytes.NewBufferString(`{
		"name":"checkout", "prometheusUrl":"http://prometheus.example.com",
		"loadQuery":"load", "latencyQuery":"latency", "intervalSeconds":60,
		"importStart":"2026-07-01T00:00:00Z", "importEnd":"2026-07-02T00:00:00Z"
	}`)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/services", body))

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Eventually(t, func() bool {
		jobs := imports.listForService(1)
		return len(jobs) == 1 && jobs[0].State == "complete" && jobs[0].Progress == 100
	}, time.Second, time.Millisecond)
}

func TestSleepHandlerReturnsSuccessfulPingAfterDelay(t *testing.T) {
	delay := 20 * time.Millisecond
	request := httptest.NewRequest(http.MethodGet, "/sleep", nil)
	recorder := httptest.NewRecorder()

	start := time.Now()
	SleepHandler(delay).ServeHTTP(recorder, request)
	elapsed := time.Since(start)

	assert.GreaterOrEqual(t, elapsed, delay)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "text/plain; charset=utf-8", recorder.Header().Get("Content-Type"))
	assert.Equal(t, sleep_message, recorder.Body.String())
}

func TestSleepHandlerRejectsNonGetMethods(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/sleep", nil)
	recorder := httptest.NewRecorder()

	SleepHandler(time.Second).ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusMethodNotAllowed, recorder.Code)
	assert.Equal(t, http.MethodGet, recorder.Header().Get("Allow"))
}

func TestSleepHandlerStopsWhenRequestIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/sleep", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	delay := 200 * time.Millisecond

	start := time.Now()
	SleepHandler(delay).ServeHTTP(recorder, request)

	assert.Less(t, time.Since(start), delay)
	assert.Empty(t, recorder.Header())
	assert.Empty(t, recorder.Body.String())
}
