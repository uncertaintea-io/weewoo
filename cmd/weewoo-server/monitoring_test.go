package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/collection"
	"github.com/uncertaintea-io/weewoo/internal/config"
)

type cancellableImportCollector struct {
	started chan struct{}
}

func (c *cancellableImportCollector) Schedule(*config.Service) error { return nil }
func (c *cancellableImportCollector) Unschedule(int)                 {}
func (c *cancellableImportCollector) Import(ctx context.Context, _ *config.Service, _, _ time.Time) error {
	close(c.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestCancelImportDoesNotDegradeHealthyService(t *testing.T) {
	service := &config.Service{Id: 42}
	monitor := newTrackingMonitor()
	monitor.record(service.Id, "healthy", "collection_succeeded", "collected", time.Now().UTC())
	tracker := &cancellableImportCollector{started: make(chan struct{})}
	imports := newImportManager(tracker, monitor)
	job := imports.start(service, time.Now().Add(-time.Hour), time.Now())
	<-tracker.started
	handler := NewServiceAPIHandler(config.NewFakeConfig(), tracker, monitor, imports, http.DefaultClient)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/imports/%d/cancel", job.ID), nil))

	require.Equal(t, http.StatusNoContent, recorder.Code)
	require.Eventually(t, func() bool {
		return imports.get(job.ID).State == "cancelled"
	}, time.Second, time.Millisecond)
	status := monitor.status(service.Id)
	assert.Equal(t, "healthy", status.State)
	require.NotEmpty(t, status.Activity)
	assert.Equal(t, "import_cancelled", status.Activity[0].Type)
}

type failingImportCollector struct{}

func (*failingImportCollector) Schedule(*config.Service) error { return nil }
func (*failingImportCollector) Unschedule(int)                 {}
func (*failingImportCollector) Import(context.Context, *config.Service, time.Time, time.Time) error {
	return fmt.Errorf("historical data unavailable")
}

func TestFailedImportDoesNotDegradeHealthyLiveMonitoring(t *testing.T) {
	service := &config.Service{Id: 42}
	monitor := newTrackingMonitor()
	monitor.record(service.Id, "healthy", "collection_succeeded", "collected", time.Now().UTC())
	imports := newImportManager(&failingImportCollector{}, monitor)

	job := imports.start(service, time.Now().Add(-time.Hour), time.Now())

	require.Eventually(t, func() bool {
		return imports.get(job.ID).State == "failed"
	}, time.Second, time.Millisecond)
	status := monitor.status(service.Id)
	assert.Equal(t, "healthy", status.State)
	require.NotEmpty(t, status.Activity)
	assert.Equal(t, "import_failed", status.Activity[0].Type)
}

func TestRecoveredHistoryDoesNotOverwriteHealthyLiveMonitoring(t *testing.T) {
	monitor := newTrackingMonitor()
	now := time.Now().UTC()
	monitor.record(42, "healthy", "collection_succeeded", "collected", now)

	monitor.handleCollectorEvent(collection.CollectorEvent{
		ServiceID: 42,
		Kind:      "collection_backlog_recovered",
		Message:   "Recovered a historical collection window",
		At:        now.Add(time.Second),
	})

	status := monitor.status(42)
	assert.Equal(t, "healthy", status.State)
	require.NotEmpty(t, status.Activity)
	assert.Equal(t, "collection_backlog_recovered", status.Activity[0].Type)
}
