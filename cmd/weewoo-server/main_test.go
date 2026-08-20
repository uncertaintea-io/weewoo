// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

type serviceRoundTripFunc func(*http.Request) (*http.Response, error)

func (f serviceRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestDefaultConfigPath(t *testing.T) {
	t.Run("uses local config by default", func(t *testing.T) {
		t.Setenv("WEEWOO_CONFIG", "")
		assert.Equal(t, "config.yaml", defaultConfigPath())
	})

	t.Run("uses environment override", func(t *testing.T) {
		t.Setenv("WEEWOO_CONFIG", "/run/secrets/weewoo.yaml")
		assert.Equal(t, "/run/secrets/weewoo.yaml", defaultConfigPath())
	})
}

func (c *fakeServiceCollector) Stop() {}
func (c *fakeServiceCollector) Unschedule(serviceID int) {
	c.unscheduled = serviceID
}
func (c *fakeServiceCollector) Schedule(service *config.Service) error {
	c.scheduled = service
	return nil
}

func newTestServiceAPIHandler(cfg config.Config, tracker serviceCollector, monitor *trackingMonitor) http.Handler {
	return NewServiceAPIHandler(serviceAPIOptions{
		Config: cfg, Tracker: tracker, Monitor: monitor,
		Imports: newImportManager(tracker, monitor, nil), HTTPClient: http.DefaultClient,
	})
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

func (c *fakeServiceCollector) Import(_ context.Context, service *config.Service, start, end time.Time, progress collection.ImportProgressHandler) (collection.ImportSummary, error) {
	c.imported, c.start, c.end = service, start, end
	summary := collection.ImportSummary{TotalWindows: 1, ImportedWindows: 1}
	if progress != nil {
		progress(collection.ImportProgress{Percent: 100, ImportSummary: summary})
	}
	return summary, nil
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
	apiHandler := newTestServiceAPIHandler(cfg, tracker, monitor)
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

func TestServiceAPIReportsTrackingStatusAndDeletesService(t *testing.T) {
	cfg := config.NewFakeConfig()
	service := &config.Service{Name: "checkout", PrometheusURL: "http://prometheus.example.com", LoadQuery: "load", LatencyQuery: "latency", Interval: time.Minute}
	require.NoError(t, cfg.WriteService(service))
	monitor := newTrackingMonitor()
	now := time.Now().UTC()
	monitor.record(service.Id, "healthy", "collection_succeeded", "collected", now)
	tracker := &fakeServiceCollector{}
	imports := newImportManager(tracker, monitor, nil)
	handler := NewServiceAPIHandler(serviceAPIOptions{
		Config: cfg, Tracker: tracker, Monitor: monitor,
		Imports: imports, HTTPClient: http.DefaultClient,
	})

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

func TestServiceAPIReturnsEmptyArrayForServiceWithoutConfigurationHistory(t *testing.T) {
	cfg := config.NewFakeConfig()
	service := &config.Service{
		Name: "checkout", PrometheusURL: "http://prometheus.example.com",
		LoadQuery: "load", LatencyQuery: "latency", Interval: time.Minute,
	}
	require.NoError(t, cfg.WriteService(service))
	monitor := newTrackingMonitor()
	tracker := &fakeServiceCollector{}
	handler := newTestServiceAPIHandler(cfg, tracker, monitor)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/services/1/history", nil))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.JSONEq(t, `[]`, recorder.Body.String())
}

func TestServiceAPIUpdatesConfigurationAsANewAuditedGeneration(t *testing.T) {
	cfg := config.NewFakeConfig()
	service := &config.Service{
		Name: "checkout", PrometheusURL: "http://prometheus.example.com",
		LoadQuery: "old_load", LatencyQuery: "old_latency", Interval: time.Minute,
	}
	require.NoError(t, cfg.WriteService(service))
	monitor := newTrackingMonitor()
	tracker := &fakeServiceCollector{}
	handler := newTestServiceAPIHandler(cfg, tracker, monitor)
	body := bytes.NewBufferString(`{
		"name":"checkout", "prometheusUrl":"http://prometheus.example.com",
		"loadQuery":"new_load", "latencyQuery":"new_latency",
		"intervalSeconds":60, "revision":1
	}`)
	request := httptest.NewRequest(http.MethodPut, "/api/services/1", body)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	updated, err := cfg.ReadService(service.Id)
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.Revision)
	assert.Equal(t, int64(2), updated.Generation)
	assert.Equal(t, "new_load", updated.LoadQuery)
	require.NotZero(t, updated.BaselineResetAt)
	history, err := cfg.ReadServiceHistory(service.Id)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, "anonymous", history[0].ChangedBy)
	assert.Equal(t, int64(1), history[0].PreviousRevision)
	assert.Equal(t, int64(2), history[0].NewRevision)
	assert.True(t, history[0].Material)
}

func TestServiceAPIResetsBaselineForNewServiceVersion(t *testing.T) {
	cfg := config.NewFakeConfig()
	service := &config.Service{
		Name: "checkout", PrometheusURL: "http://prometheus.example.com",
		LoadQuery: "load", LatencyQuery: "latency", Interval: time.Minute,
	}
	require.NoError(t, cfg.WriteService(service))
	monitor := newTrackingMonitor()
	tracker := &fakeServiceCollector{}
	handler := newTestServiceAPIHandler(cfg, tracker, monitor)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/services/1/baseline-reset", nil))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	updated, err := cfg.ReadService(service.Id)
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.Revision)
	assert.Equal(t, int64(2), updated.Generation)
	require.NotZero(t, updated.BaselineResetAt)
	require.NotNil(t, tracker.scheduled)
	assert.Equal(t, int64(2), tracker.scheduled.Generation)
	history, err := cfg.ReadServiceHistory(service.Id)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.True(t, history[0].Material)
	assert.Equal(t, int64(1), history[0].Previous.Generation)
	assert.Equal(t, int64(2), history[0].Current.Generation)
}

func TestServiceAPIPauseAndResumeAreRevisionedAndAudited(t *testing.T) {
	cfg := config.NewFakeConfig()
	service := &config.Service{
		Name: "checkout", PrometheusURL: "http://prometheus.example.com",
		LoadQuery: "load", LatencyQuery: "latency", Interval: time.Minute,
	}
	require.NoError(t, cfg.WriteService(service))
	monitor := newTrackingMonitor()
	tracker := &fakeServiceCollector{}
	handler := newTestServiceAPIHandler(cfg, tracker, monitor)

	pause := httptest.NewRecorder()
	handler.ServeHTTP(pause, httptest.NewRequest(http.MethodPost, "/api/services/1/pause", nil))
	require.Equal(t, http.StatusOK, pause.Code, pause.Body.String())

	resume := httptest.NewRecorder()
	handler.ServeHTTP(resume, httptest.NewRequest(http.MethodPost, "/api/services/1/resume", nil))
	require.Equal(t, http.StatusOK, resume.Code, resume.Body.String())

	updated, err := cfg.ReadService(service.Id)
	require.NoError(t, err)
	assert.False(t, updated.Paused)
	assert.Equal(t, int64(3), updated.Revision)
	assert.Equal(t, int64(1), updated.Generation)
	history, err := cfg.ReadServiceHistory(service.Id)
	require.NoError(t, err)
	require.Len(t, history, 2)
	assert.False(t, history[0].Material)
	assert.False(t, history[1].Material)
}

func TestServiceAPITestsBothQueriesBeforeActivation(t *testing.T) {
	client := &http.Client{Transport: serviceRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1,"2.5"],[2,"3.5"]]}]}}`,
			)),
		}, nil
	})}
	cfg := config.NewFakeConfig()
	tracker := &fakeServiceCollector{}
	handler := NewServiceAPIHandler(serviceAPIOptions{
		Config: cfg, Tracker: tracker, Monitor: newTrackingMonitor(),
		Imports: newImportManager(tracker, newTrackingMonitor(), nil), HTTPClient: client,
	})
	body := bytes.NewBufferString(fmt.Sprintf(`{
		"name":"checkout", "prometheusUrl":%q, "loadQuery":"load",
		"latencyQuery":"latency", "intervalSeconds":60
	}`, "http://prometheus.example.com"))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/services/test", body))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var result struct {
		LoadQuery struct {
			Valid   bool
			Samples int
		} `json:"loadQuery"`
		LatencyQuery struct {
			Valid   bool
			Samples int
		} `json:"latencyQuery"`
	}
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&result))
	assert.True(t, result.LoadQuery.Valid)
	assert.Equal(t, 2, result.LoadQuery.Samples)
	assert.True(t, result.LatencyQuery.Valid)
	assert.Equal(t, 2, result.LatencyQuery.Samples)
}

func TestServiceAPICreatesBackgroundImport(t *testing.T) {
	cfg := config.NewFakeConfig()
	monitor := newTrackingMonitor()
	tracker := &fakeServiceCollector{}
	imports := newImportManager(tracker, monitor, nil)
	handler := NewServiceAPIHandler(serviceAPIOptions{
		Config: cfg, Tracker: tracker, Monitor: monitor,
		Imports: imports, HTTPClient: http.DefaultClient,
	})
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

func TestServiceAPIRejectsHistoricalRangeUnlessStartIsBeforeEnd(t *testing.T) {
	cfg := config.NewFakeConfig()
	monitor := newTrackingMonitor()
	tracker := &fakeServiceCollector{}
	handler := newTestServiceAPIHandler(cfg, tracker, monitor)
	body := bytes.NewBufferString(`{
		"name":"checkout", "prometheusUrl":"http://prometheus.example.com",
		"loadQuery":"load", "latencyQuery":"latency", "intervalSeconds":60,
		"importStart":"2026-07-30T00:00:00Z", "importEnd":"2026-07-29T00:00:00Z"
	}`)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/services", body))

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Equal(t, "importStart must be before importEnd\n", recorder.Body.String())
	services, err := cfg.ReadAllServices()
	require.NoError(t, err)
	assert.Empty(t, services)
	assert.Nil(t, tracker.scheduled)
}

func TestServiceAPIRejectsIntervalBelowConfiguredMinimum(t *testing.T) {
	cfg := config.NewFakeConfig()
	monitor := newTrackingMonitor()
	tracker := &fakeServiceCollector{}
	handler := newTestServiceAPIHandler(cfg, tracker, monitor)
	// The configured minimum is 15 seconds, so this request exercises the
	// validation path with an interval one second below the allowed boundary.
	body := bytes.NewBufferString(`{
		"name":"checkout", "prometheusUrl":"http://prometheus.example.com",
		"loadQuery":"load", "latencyQuery":"latency", "intervalSeconds":14
	}`)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/services", body))

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Equal(t, "intervalSeconds must be at least 15\n", recorder.Body.String())
	// Rejected service creation must have no side effects: it is neither saved
	// in configuration nor handed to the live collector for scheduling.
	services, err := cfg.ReadAllServices()
	require.NoError(t, err)
	assert.Empty(t, services)
	assert.Nil(t, tracker.scheduled)
}

func TestServiceAPIAcceptsConfiguredMinimumInterval(t *testing.T) {
	cfg := config.NewFakeConfig()
	monitor := newTrackingMonitor()
	tracker := &fakeServiceCollector{}
	handler := newTestServiceAPIHandler(cfg, tracker, monitor)
	body := bytes.NewBufferString(`{
		"name":"checkout", "prometheusUrl":"http://prometheus.example.com",
		"loadQuery":"load", "latencyQuery":"latency", "intervalSeconds":15
	}`)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/services", body))

	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	service, err := cfg.ReadService(1)
	require.NoError(t, err)
	assert.Equal(t, config.MinimumServiceInterval, service.Interval)
}
