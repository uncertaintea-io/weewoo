package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/config"
)

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
	}, services[0])
}

func TestNewListAllServicesHandlerRejectsNonGetMethods(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/services", nil)
	recorder := httptest.NewRecorder()

	NewListAllServicesHandler(config.NewFakeConfig()).ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusMethodNotAllowed, recorder.Code)
	assert.Equal(t, http.MethodGet, recorder.Header().Get("Allow"))
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
	assert.Equal(t, "pong\n", recorder.Body.String())
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
