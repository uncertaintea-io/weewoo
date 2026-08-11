package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/alerting"
	"github.com/uncertaintea-io/weewoo/internal/collection"
	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

const prometheusTestTimeout = 15 * time.Second

type serviceAPI struct {
	cfg        config.Config
	tracker    serviceCollector
	monitor    *trackingMonitor
	imports    *importManager
	httpClient *http.Client
	alerts     *alerting.Manager
	models     modelStatusReader
}

type modelStatusReader interface {
	ModelStatus(context.Context, *config.Service, int) (modelStatus, error)
}

type serviceAPIOptions struct {
	Config      config.Config
	Tracker     serviceCollector
	Monitor     *trackingMonitor
	Imports     *importManager
	HTTPClient  *http.Client
	Alerts      *alerting.Manager
	ModelStatus modelStatusReader
}

func NewServiceAPIHandler(options serviceAPIOptions) http.Handler {
	return &serviceAPI{
		cfg:        options.Config,
		tracker:    options.Tracker,
		monitor:    options.Monitor,
		imports:    options.Imports,
		httpClient: options.HTTPClient,
		alerts:     options.Alerts,
		models:     options.ModelStatus,
	}
}

func (a *serviceAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/"), "/")
	if path == "services/test" {
		a.testConnection(w, r)
		return
	}
	if path == "services" {
		switch r.Method {
		case http.MethodGet:
			a.list(w)
		case http.MethodPost:
			a.create(w, r)
		default:
			methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
		}
		return
	}
	if strings.HasPrefix(path, "services/") && (strings.HasSuffix(path, "/pause") || strings.HasSuffix(path, "/resume")) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		parts := strings.Split(path, "/")
		if len(parts) != 3 {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		id, err := strconv.Atoi(parts[1])
		if err != nil || id <= 0 {
			http.Error(w, "invalid service ID", http.StatusBadRequest)
			return
		}
		service, err := a.cfg.ReadService(id)
		if err != nil {
			http.Error(w, "service not found", http.StatusNotFound)
			return
		}
		updated := *service
		updated.Paused = parts[2] == "pause"
		if err := a.cfg.UpdateService(r.Context(), &updated, service.Revision, requestActor(r)); err != nil {
			if errors.Is(err, config.ErrServiceConflict) {
				http.Error(w, "service was changed by another user; retry the operation", http.StatusConflict)
				return
			}
			if parts[2] == "pause" {
				http.Error(w, "failed to save paused state", http.StatusInternalServerError)
			} else {
				http.Error(w, "failed to save resumed state", http.StatusInternalServerError)
			}
			return
		}
		service = &updated
		if parts[2] == "pause" {
			a.tracker.Unschedule(id)
			if a.alerts != nil {
				_ = a.alerts.CloseService(r.Context(), id, "monitoring_paused", time.Now().UTC())
			}
			a.monitor.record(id, "paused", "tracking_paused", "Prometheus collection was paused", time.Now().UTC())
		} else {
			if err := a.tracker.Schedule(service); err != nil {
				http.Error(w, "tracking could not be resumed", http.StatusInternalServerError)
				return
			}
		}
		writeJSON(w, http.StatusOK, a.response(service))
		return
	}
	if strings.HasPrefix(path, "services/") && strings.HasSuffix(path, "/baseline-reset") {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		parts := strings.Split(path, "/")
		if len(parts) != 3 {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		id, err := strconv.Atoi(parts[1])
		if err != nil || id <= 0 {
			http.Error(w, "invalid service ID", http.StatusBadRequest)
			return
		}
		service, err := a.cfg.ReadService(id)
		if err != nil {
			http.Error(w, "service not found", http.StatusNotFound)
			return
		}
		reset, err := a.cfg.ResetServiceBaseline(r.Context(), id, service.Revision, requestActor(r))
		if errors.Is(err, config.ErrServiceConflict) {
			http.Error(w, "service was changed by another user; reload and try again", http.StatusConflict)
			return
		}
		if errors.Is(err, config.ErrServiceNotFound) {
			http.Error(w, "service not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "failed to reset service baseline", http.StatusInternalServerError)
			return
		}
		if a.alerts != nil {
			_ = a.alerts.CloseService(r.Context(), id, "service_version_changed", time.Now().UTC())
		}
		if !reset.Paused {
			if err := a.tracker.Schedule(reset); err != nil {
				a.monitor.record(id, "degraded", "baseline_reset_apply_failed", "Baseline reset, but tracking could not be restarted", time.Now().UTC())
				writeJSON(w, http.StatusAccepted, a.response(reset))
				return
			}
			a.monitor.activateRevision(id, reset.Revision)
			a.monitor.record(id, "collecting", "baseline_reset", fmt.Sprintf("New service version; learning generation %d", reset.Generation), time.Now().UTC())
		}
		writeJSON(w, http.StatusOK, a.response(reset))
		return
	}
	if strings.HasPrefix(path, "services/") && strings.HasSuffix(path, "/history") {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		idText := strings.TrimSuffix(strings.TrimPrefix(path, "services/"), "/history")
		id, err := strconv.Atoi(idText)
		if err != nil || id <= 0 {
			http.Error(w, "invalid service ID", http.StatusBadRequest)
			return
		}
		a.history(w, id)
		return
	}
	if strings.HasPrefix(path, "services/") {
		id, err := strconv.Atoi(strings.TrimPrefix(path, "services/"))
		if err != nil || id <= 0 {
			http.Error(w, "invalid service ID", http.StatusBadRequest)
			return
		}
		switch r.Method {
		case http.MethodGet:
			a.detail(w, id)
		case http.MethodPut:
			a.update(w, r, id)
		case http.MethodDelete:
			a.delete(w, id)
		default:
			methodNotAllowed(w, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
		}
		return
	}
	if strings.HasPrefix(path, "imports/") && strings.HasSuffix(path, "/cancel") {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		idText := strings.TrimSuffix(strings.TrimPrefix(path, "imports/"), "/cancel")
		id, err := strconv.ParseInt(idText, 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid import ID", http.StatusBadRequest)
			return
		}
		if err := a.imports.cancel(id); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.NotFound(w, r)
}

func (a *serviceAPI) history(w http.ResponseWriter, id int) {
	history, err := a.cfg.ReadServiceHistory(id)
	if err != nil {
		http.Error(w, "failed to read service history", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func decodeServiceRequest(w http.ResponseWriter, r *http.Request) (createServiceRequest, error) {
	var request createServiceRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, fmt.Errorf("invalid JSON request")
	}
	if err := validateCreateService(request); err != nil {
		return request, err
	}
	return request, nil
}

func serviceFromRequest(request createServiceRequest, id int) *config.Service {
	return &config.Service{Id: id, Name: request.Name, PrometheusURL: request.PrometheusURL, LoadQuery: request.LoadQuery, LatencyQuery: request.LatencyQuery, Interval: time.Duration(request.IntervalSeconds) * time.Second}
}

func (a *serviceAPI) response(service *config.Service) serviceResponse {
	response := newServiceResponse(service)
	response.Tracking = a.monitor.status(service.Id)
	response.Imports = a.imports.listForService(service.Id)
	if a.models != nil {
		if status, err := a.models.ModelStatus(context.Background(), service, collection.TimeOfDayIndicator); err == nil {
			response.TimeOfDayModel = status
		}
	}
	return response
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (a *serviceAPI) list(w http.ResponseWriter) {
	services, err := a.cfg.ReadAllServices()
	if err != nil {
		http.Error(w, "failed to read services", http.StatusInternalServerError)
		return
	}
	response := make([]serviceResponse, 0, len(services))
	for _, service := range services {
		response = append(response, a.response(service))
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *serviceAPI) detail(w http.ResponseWriter, id int) {
	service, err := a.cfg.ReadService(id)
	if err != nil {
		http.Error(w, "service not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, a.response(service))
}

func (a *serviceAPI) create(w http.ResponseWriter, r *http.Request) {
	request, err := decodeServiceRequest(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	service := serviceFromRequest(request, 0)
	if err := a.cfg.WriteService(service); err != nil {
		http.Error(w, "failed to create service", http.StatusInternalServerError)
		return
	}
	if err := a.tracker.Schedule(service); err != nil {
		http.Error(w, "service created, but tracking could not be started", http.StatusInternalServerError)
		return
	}
	a.monitor.activateRevision(service.Id, service.Revision)
	if request.ImportStart != nil {
		a.imports.start(service, *request.ImportStart, *request.ImportEnd)
	}
	w.Header().Set("Location", fmt.Sprintf("/api/services/%d", service.Id))
	writeJSON(w, http.StatusCreated, a.response(service))
}

func (a *serviceAPI) update(w http.ResponseWriter, r *http.Request, id int) {
	existing, err := a.cfg.ReadService(id)
	if err != nil {
		http.Error(w, "service not found", http.StatusNotFound)
		return
	}
	request, err := decodeServiceRequest(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	service := serviceFromRequest(request, id)
	service.Paused = existing.Paused
	expectedRevision := request.Revision
	if expectedRevision == 0 {
		expectedRevision = existing.Revision
	}
	if err := a.cfg.UpdateService(r.Context(), service, expectedRevision, requestActor(r)); err != nil {
		if errors.Is(err, config.ErrServiceConflict) {
			http.Error(w, "service was changed by another user; reload and try again", http.StatusConflict)
			return
		}
		if errors.Is(err, config.ErrServiceNotFound) {
			http.Error(w, "service not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to update service", http.StatusInternalServerError)
		return
	}
	if service.Generation != existing.Generation && a.alerts != nil {
		_ = a.alerts.CloseService(r.Context(), id, "configuration_changed", time.Now().UTC())
	}
	if !service.Paused {
		if err := a.tracker.Schedule(service); err != nil {
			a.monitor.record(id, "degraded", "configuration_apply_failed", "Configuration saved, but tracking could not be restarted", time.Now().UTC())
			writeJSON(w, http.StatusAccepted, a.response(service))
			return
		}
		a.monitor.activateRevision(id, service.Revision)
		a.monitor.record(id, "collecting", "configuration_activated", fmt.Sprintf("Configuration revision %d activated; learning generation %d", service.Revision, service.Generation), time.Now().UTC())
	}
	writeJSON(w, http.StatusOK, a.response(service))
}

func requestActor(r *http.Request) string {
	if r.TLS != nil && len(r.TLS.VerifiedChains) > 0 && len(r.TLS.PeerCertificates) > 0 {
		if commonName := r.TLS.PeerCertificates[0].Subject.CommonName; commonName != "" {
			return commonName
		}
	}
	return "anonymous"
}

func (a *serviceAPI) delete(w http.ResponseWriter, id int) {
	service, err := a.cfg.ReadService(id)
	if err != nil {
		http.Error(w, "service not found", http.StatusNotFound)
		return
	}
	if err := a.cfg.DeleteService(id); err != nil {
		http.Error(w, "service not found", http.StatusNotFound)
		return
	}
	a.tracker.Unschedule(id)
	if a.alerts != nil {
		_ = a.alerts.CloseService(context.Background(), service.Id, "service_removed", time.Now().UTC())
	}
	a.monitor.remove(id)
	w.WriteHeader(http.StatusNoContent)
}

func (a *serviceAPI) testConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	request, err := decodeServiceRequest(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), prometheusTestTimeout)
	defer cancel()
	end := time.Now().UTC().Truncate(time.Minute)
	start := end.Add(-5 * time.Minute)
	loadPoints, loadErr := collection.QueryPrometheusRangePoints(ctx, a.httpClient, request.PrometheusURL, request.LoadQuery, start, end, time.Minute)
	latencySamples, latencyErr := collection.QueryPrometheusRangeSamples(ctx, a.httpClient, request.PrometheusURL, request.LatencyQuery, start, end, time.Minute)

	type queryResult struct {
		Valid   bool    `json:"valid"`
		Samples int     `json:"samples"`
		Latest  float64 `json:"latest,omitempty"`
		Error   string  `json:"error,omitempty"`
	}

	resultForPoints := func(points []collection.PrometheusPoint, err error) queryResult {
		if err != nil {
			return queryResult{Error: err.Error()}
		}
		return queryResult{Valid: true, Samples: len(points), Latest: points[len(points)-1].Value}
	}

	resultForSamples := func(samples []ecdf.Sample, err error) queryResult {
		if err != nil {
			return queryResult{Error: err.Error()}
		}
		total := 0
		for _, sample := range samples {
			total += int(sample.Count)
		}
		result := queryResult{Valid: true, Samples: total}
		if len(samples) > 0 {
			result.Latest = samples[len(samples)-1].Value
		}
		return result
	}

	response := struct {
		Message      string      `json:"message"`
		LoadQuery    queryResult `json:"loadQuery"`
		LatencyQuery queryResult `json:"latencyQuery"`
	}{
		Message:      "Connection and queries succeeded",
		LoadQuery:    resultForPoints(loadPoints, loadErr),
		LatencyQuery: resultForSamples(latencySamples, latencyErr),
	}
	if loadErr != nil || latencyErr != nil {
		response.Message = "One or more queries failed validation"
		writeJSON(w, http.StatusUnprocessableEntity, response)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
