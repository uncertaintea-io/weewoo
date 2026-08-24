// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/collection"
	"github.com/uncertaintea-io/weewoo/internal/config"
)

type durableImportJobStore struct {
	mu     sync.Mutex
	nextID int64
	jobs   map[int64]importJob
}

func newDurableImportJobStore() *durableImportJobStore {
	return &durableImportJobStore{nextID: 1, jobs: make(map[int64]importJob)}
}

func (s *durableImportJobStore) create(_ context.Context, job *importJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job.ID = s.nextID
	s.nextID++
	copy := *job
	copy.cancel = nil
	s.jobs[job.ID] = copy
	return nil
}

func (s *durableImportJobStore) update(_ context.Context, job importJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.jobs[job.ID]
	if !ok || stored.OwnerID != job.OwnerID {
		return fmt.Errorf("historical import job %d not found or no longer owned", job.ID)
	}
	job.cancel = nil
	s.jobs[job.ID] = job
	return nil
}

func (s *durableImportJobStore) list(context.Context) ([]importJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := make([]importJob, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (s *durableImportJobStore) markInterruptedIfStale(_ context.Context, id int64, cutoff time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return false, fmt.Errorf("historical import job %d not found", id)
	}
	if job.State != "queued" && job.State != "running" && job.State != "building" {
		return false, nil
	}
	if job.HeartbeatAt != nil && !job.HeartbeatAt.Before(cutoff) {
		return false, nil
	}
	job.State = "failed"
	job.Error = "Historical import interrupted by server restart"
	job.EndedAt = interruptedImportEnd()
	job.OwnerID = ""
	s.jobs[id] = job
	return true, nil
}

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
	imports := newImportManager(tracker, monitor, nil)
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
	imports := newImportManager(&failingImportCollector{}, monitor, nil)

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
	imports := newImportManager(&gapImportCollector{}, newTrackingMonitor(), nil)

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

func TestSuccessfulImportBuildsJECDFBeforeCompleting(t *testing.T) {
	service := &config.Service{Id: 42}
	built := make(chan int, 1)
	imports := newImportManager(&gapImportCollector{}, newTrackingMonitor(), func(_ context.Context, serviceID int) error {
		built <- serviceID
		return nil
	})

	job := imports.start(service, time.Now().Add(-time.Hour), time.Now())

	require.Eventually(t, func() bool {
		return imports.get(job.ID).State == "complete_with_gaps"
	}, time.Second, time.Millisecond)
	select {
	case serviceID := <-built:
		assert.Equal(t, service.Id, serviceID)
	default:
		t.Fatal("JECDF build was not triggered")
	}
	status := imports.monitor.status(service.Id)
	require.NotEmpty(t, status.Activity)
	assert.Equal(t, "import_completed", status.Activity[0].Type)
	assert.Equal(t, "jecdf_built", status.Activity[1].Type)
}

func TestPostImportJECDFFailureFailsTheImportWorkflow(t *testing.T) {
	service := &config.Service{Id: 42}
	imports := newImportManager(&gapImportCollector{}, newTrackingMonitor(), func(context.Context, int) error {
		return fmt.Errorf("builder unavailable")
	})

	job := imports.start(service, time.Now().Add(-time.Hour), time.Now())

	require.Eventually(t, func() bool {
		return imports.get(job.ID).State == "failed"
	}, time.Second, time.Millisecond)
	failed := imports.get(job.ID)
	assert.Contains(t, failed.Error, "import succeeded but JECDF build failed")
	status := imports.monitor.status(service.Id)
	require.NotEmpty(t, status.Activity)
	assert.Equal(t, "jecdf_build_failed", status.Activity[0].Type)
}

func TestFailedImportDoesNotBuildJECDF(t *testing.T) {
	service := &config.Service{Id: 42}
	built := make(chan struct{}, 1)
	imports := newImportManager(&failingImportCollector{}, newTrackingMonitor(), func(context.Context, int) error {
		built <- struct{}{}
		return nil
	})

	job := imports.start(service, time.Now().Add(-time.Hour), time.Now())

	require.Eventually(t, func() bool {
		return imports.get(job.ID).State == "failed"
	}, time.Second, time.Millisecond)
	select {
	case <-built:
		t.Fatal("failed import triggered a JECDF build")
	default:
	}
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

func TestCompletedHistoricalImportSurvivesManagerRestart(t *testing.T) {
	store := newDurableImportJobStore()
	service := &config.Service{Id: 42}
	first := newImportManager(&gapImportCollector{}, newTrackingMonitor(), nil, store)
	start := time.Now().UTC().Add(-time.Hour)
	job := first.start(service, start, start.Add(time.Hour))
	require.Eventually(t, func() bool {
		return first.get(job.ID).State == "complete_with_gaps"
	}, time.Second, time.Millisecond)

	restarted := newImportManager(&gapImportCollector{}, newTrackingMonitor(), nil, store)
	jobs := restarted.listForService(service.Id)

	require.Len(t, jobs, 1)
	assert.Equal(t, "complete_with_gaps", jobs[0].State)
	assert.Equal(t, 100, jobs[0].Progress)
	assert.Equal(t, start, jobs[0].RangeStart)
	assert.Equal(t, start.Add(time.Hour), jobs[0].RangeEnd)
}

func TestRunningHistoricalImportIsMarkedInterruptedAfterRestart(t *testing.T) {
	store := newDurableImportJobStore()
	start := time.Now().UTC().Add(-time.Hour)
	job := importJob{
		ServiceID: 42, State: "running", RangeStart: start, RangeEnd: start.Add(time.Hour), StartedAt: start,
	}
	require.NoError(t, store.create(context.Background(), &job))

	restarted := newImportManager(&gapImportCollector{}, newTrackingMonitor(), nil, store)
	loaded := restarted.get(job.ID)

	assert.Equal(t, "failed", loaded.State)
	assert.Equal(t, "Historical import interrupted by server restart", loaded.Error)
	assert.NotNil(t, loaded.EndedAt)
}

func TestRunningHistoricalImportOwnedByAnotherReplicaIsNotMarkedInterrupted(t *testing.T) {
	store := newDurableImportJobStore()
	service := &config.Service{Id: 42}
	collector := &cancellableImportCollector{started: make(chan struct{})}
	owner := newImportManager(collector, newTrackingMonitor(), nil, store)
	job := owner.start(service, time.Now().UTC().Add(-time.Hour), time.Now().UTC())
	<-collector.started
	require.Eventually(t, func() bool {
		return owner.get(job.ID).State == "running"
	}, time.Second, time.Millisecond)

	newImportManager(&gapImportCollector{}, newTrackingMonitor(), nil, store)
	jobs, err := store.list(context.Background())
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, "running", jobs[0].State)

	require.NoError(t, owner.cancel(job.ID))
}
