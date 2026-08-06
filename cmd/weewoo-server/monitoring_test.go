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
func (c *cancellableImportCollector) Import(ctx context.Context, _ *config.Service, _, _ time.Time, _ collection.ImportProgressHandler) (collection.ImportSummary, error) {
	close(c.started)
	<-ctx.Done()
	return collection.ImportSummary{}, ctx.Err()
}

func TestCancelImportDoesNotDegradeHealthyService(t *testing.T) {
	service := &config.Service{Id: 42}
	monitor := newTrackingMonitor()
	monitor.record(service.Id, "healthy", "collection_succeeded", "collected", time.Now().UTC())
	tracker := &cancellableImportCollector{started: make(chan struct{})}
	imports := newImportManager(tracker, monitor)
	job := imports.start(service, time.Now().Add(-time.Hour), time.Now())
	<-tracker.started
	handler := NewServiceAPIHandler(serviceAPIOptions{
		Config: config.NewFakeConfig(), HTTPClient: http.DefaultClient,
		Tracker: tracker, Monitor: monitor, Imports: imports,
	})
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
func (*failingImportCollector) Import(context.Context, *config.Service, time.Time, time.Time, collection.ImportProgressHandler) (collection.ImportSummary, error) {
	return collection.ImportSummary{}, fmt.Errorf("historical data unavailable")
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

type gapImportCollector struct{}

func (*gapImportCollector) Schedule(*config.Service) error { return nil }
func (*gapImportCollector) Unschedule(int)                 {}
func (*gapImportCollector) Import(_ context.Context, _ *config.Service, _, _ time.Time, progress collection.ImportProgressHandler) (collection.ImportSummary, error) {
	summary := collection.ImportSummary{TotalWindows: 100, ImportedWindows: 75, GapWindows: 25}
	progress(collection.ImportProgress{Percent: 100, ImportSummary: summary})
	return summary, nil
}

func TestImportJobCompletesWithMonitoringGapSummary(t *testing.T) {
	service := &config.Service{Id: 42}
	imports := newImportManager(&gapImportCollector{}, newTrackingMonitor())

	job := imports.start(service, time.Now().Add(-time.Hour), time.Now())

	require.Eventually(t, func() bool {
		return imports.get(job.ID).State == "complete_with_gaps"
	}, time.Second, time.Millisecond)
	completed := imports.get(job.ID)
	assert.Equal(t, 100, completed.Progress)
	assert.Equal(t, 100, completed.TotalWindows)
	assert.Equal(t, 75, completed.ImportedWindows)
	assert.Equal(t, 25, completed.GapWindows)
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

func TestTrackingMonitorReportsWhenCollectionStarted(t *testing.T) {
	monitor := newTrackingMonitor()
	startedAt := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	monitor.handleCollectorEvent(collection.CollectorEvent{ServiceID: 7, Kind: "tracking_started", At: startedAt})
	monitor.handleCollectorEvent(collection.CollectorEvent{ServiceID: 7, Kind: "collection_succeeded", At: startedAt.Add(time.Minute)})

	status := monitor.status(7)

	require.NotNil(t, status.StartedAt)
	assert.Equal(t, startedAt, *status.StartedAt)
}
