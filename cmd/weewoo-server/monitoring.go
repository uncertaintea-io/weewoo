package main

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/collection"
	"github.com/uncertaintea-io/weewoo/internal/config"
)

type activityEntry struct {
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

type trackingStatus struct {
	State          string          `json:"state"`
	StartedAt      *time.Time      `json:"startedAt,omitempty"`
	ActiveRevision int64           `json:"activeRevision,omitempty"`
	LastSuccess    *time.Time      `json:"lastSuccess,omitempty"`
	LastError      *time.Time      `json:"lastError,omitempty"`
	Error          string          `json:"error,omitempty"`
	Activity       []activityEntry `json:"activity"`
}

func (m *trackingMonitor) activateRevision(serviceID int, revision int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := m.services[serviceID]
	if status == nil {
		status = &trackingStatus{State: "collecting"}
		m.services[serviceID] = status
	}
	status.ActiveRevision = revision
}

type trackingMonitor struct {
	mu       sync.RWMutex
	services map[int]*trackingStatus
}

func newTrackingMonitor() *trackingMonitor {
	return &trackingMonitor{services: make(map[int]*trackingStatus)}
}

func (m *trackingMonitor) status(serviceID int) trackingStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status, ok := m.services[serviceID]
	if !ok {
		return trackingStatus{State: "pending", Activity: []activityEntry{}}
	}
	copy := *status
	copy.Activity = append([]activityEntry(nil), status.Activity...)
	return copy
}

func (m *trackingMonitor) record(serviceID int, state, kind, message string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := m.services[serviceID]
	if status == nil {
		status = &trackingStatus{State: "pending"}
		m.services[serviceID] = status
	}
	if state != "" {
		status.State = state
	}
	if kind == "tracking_started" && status.StartedAt == nil {
		status.StartedAt = &at
	}
	if kind == "collection_succeeded" {
		status.LastSuccess = &at
		status.Error = ""
	}
	if kind == "collection_failed" {
		status.LastError = &at
		status.Error = message
	}
	status.Activity = append([]activityEntry{{Type: kind, Message: message, Timestamp: at}}, status.Activity...)
	if len(status.Activity) > 50 {
		status.Activity = status.Activity[:50]
	}
}

func (m *trackingMonitor) remove(serviceID int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.services, serviceID)
}

func (m *trackingMonitor) handleCollectorEvent(event collection.CollectorEvent) {
	switch event.Kind {
	case "tracking_started":
		m.record(event.ServiceID, "collecting", event.Kind, event.Message, event.At)
	case "collection_succeeded":
		m.record(event.ServiceID, "healthy", event.Kind, event.Message, event.At)
	case "collection_failed":
		m.record(event.ServiceID, "degraded", event.Kind, event.Message, event.At)
	case "collection_delayed":
		m.record(event.ServiceID, "degraded", event.Kind, event.Message, event.At)
	case "collection_backlog_recovered":
		m.record(event.ServiceID, "", event.Kind, event.Message, event.At)
	}
}

type importJob struct {
	ID              int        `json:"id"`
	ServiceID       int        `json:"serviceId"`
	State           string     `json:"state"`
	Progress        int        `json:"progress"`
	TotalWindows    int        `json:"totalWindows"`
	ImportedWindows int        `json:"importedWindows"`
	GapWindows      int        `json:"gapWindows"`
	Error           string     `json:"error,omitempty"`
	StartedAt       time.Time  `json:"startedAt"`
	EndedAt         *time.Time `json:"endedAt,omitempty"`
	cancel          context.CancelFunc
}

type importManager struct {
	mu      sync.RWMutex
	nextID  int
	jobs    map[int]*importJob
	tracker serviceCollector
	monitor *trackingMonitor
}

func newImportManager(tracker serviceCollector, monitor *trackingMonitor) *importManager {
	return &importManager{nextID: 1, jobs: make(map[int]*importJob), tracker: tracker, monitor: monitor}
}

func (m *importManager) start(service *config.Service, start, end time.Time) importJob {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	job := &importJob{ID: m.nextID, ServiceID: service.Id, State: "queued", Progress: 0, StartedAt: time.Now().UTC(), cancel: cancel}
	m.nextID++
	m.jobs[job.ID] = job
	m.mu.Unlock()
	m.monitor.record(service.Id, "", "import_started", "Historical Prometheus import started", job.StartedAt)
	slog.Info("historical import started", "import_id", job.ID, "service_id", service.Id, "start", start, "end", end)
	go func() {
		m.update(job.ID, "running", 0, "")
		summary, err := m.tracker.Import(ctx, service, start, end, func(progress collection.ImportProgress) {
			m.updateProgress(job.ID, progress)
		})
		m.updateSummary(job.ID, summary)
		now := time.Now().UTC()
		if err != nil {
			if ctx.Err() != nil {
				message := "Historical import cancelled"
				m.finish(job.ID, "cancelled", message, now)
				m.monitor.record(service.Id, "", "import_cancelled", message, now)
				slog.Info("historical import cancelled", "import_id", job.ID, "service_id", service.Id)
				return
			}
			message := err.Error()
			m.finish(job.ID, "failed", message, now)
			m.monitor.record(service.Id, "", "import_failed", message, now)
			slog.Error("historical import failed", "import_id", job.ID, "service_id", service.Id, "error", err)
			return
		}
		state := "complete"
		message := "Historical Prometheus import completed"
		if summary.GapWindows > 0 {
			state = "complete_with_gaps"
			message = fmt.Sprintf(
				"Historical Prometheus import completed: %d windows imported, %d monitoring gaps",
				summary.ImportedWindows,
				summary.GapWindows,
			)
		}
		m.finish(job.ID, state, "", now)
		m.monitor.record(service.Id, "", "import_completed", message, now)
		slog.Info("historical import completed", "import_id", job.ID, "service_id", service.Id, "imported_windows", summary.ImportedWindows, "gap_windows", summary.GapWindows)
	}()
	return m.get(job.ID)
}

func (m *importManager) updateProgress(id int, progress collection.ImportProgress) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if job := m.jobs[id]; job != nil {
		job.Progress = progress.Percent
		job.TotalWindows = progress.TotalWindows
		job.ImportedWindows = progress.ImportedWindows
		job.GapWindows = progress.GapWindows
	}
}

func (m *importManager) updateSummary(id int, summary collection.ImportSummary) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if job := m.jobs[id]; job != nil {
		job.TotalWindows = summary.TotalWindows
		job.ImportedWindows = summary.ImportedWindows
		job.GapWindows = summary.GapWindows
	}
}

func (m *importManager) update(id int, state string, progress int, errMessage string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if job := m.jobs[id]; job != nil {
		job.State, job.Progress, job.Error = state, progress, errMessage
	}
}

func (m *importManager) finish(id int, state, errMessage string, ended time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if job := m.jobs[id]; job != nil {
		job.State, job.Error, job.EndedAt = state, errMessage, &ended
		if state == "complete" || state == "complete_with_gaps" {
			job.Progress = 100
		}
	}
}

func (m *importManager) get(id int) importJob {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if job := m.jobs[id]; job != nil {
		copy := *job
		copy.cancel = nil
		return copy
	}
	return importJob{}
}

func (m *importManager) listForService(serviceID int) []importJob {
	m.mu.RLock()
	defer m.mu.RUnlock()
	jobs := make([]importJob, 0)
	for _, job := range m.jobs {
		if job.ServiceID == serviceID {
			copy := *job
			copy.cancel = nil
			jobs = append(jobs, copy)
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID > jobs[j].ID })
	return jobs
}

func (m *importManager) cancel(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[id]
	if job == nil {
		return fmt.Errorf("import job not found")
	}
	if job.State != "queued" && job.State != "running" {
		return fmt.Errorf("import job is already %s", job.State)
	}
	job.cancel()
	return nil
}
