package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/collection"
	"github.com/uncertaintea-io/weewoo/internal/config"
)

const prometheusTestTimeout = 15 * time.Second

type serviceAPI struct {
	cfg        config.Config
	tracker    serviceCollector
	monitor    *trackingMonitor
	imports    *importManager
	httpClient *http.Client
}

func NewServiceAPIHandler(cfg config.Config, tracker serviceCollector, monitor *trackingMonitor, imports *importManager, client *http.Client) http.Handler {
	return &serviceAPI{cfg: cfg, tracker: tracker, monitor: monitor, imports: imports, httpClient: client}
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
		if parts[2] == "pause" {
			service.Paused = true
			if err := a.cfg.WriteService(service); err != nil {
				http.Error(w, "failed to save paused state", http.StatusInternalServerError)
				return
			}
			a.tracker.Unschedule(id)
			a.monitor.record(id, "paused", "tracking_paused", "Prometheus collection was paused", time.Now().UTC())
		} else {
			service.Paused = false
			if err := a.cfg.WriteService(service); err != nil {
				http.Error(w, "failed to save resumed state", http.StatusInternalServerError)
				return
			}
			if err := a.tracker.Schedule(service); err != nil {
				http.Error(w, "tracking could not be resumed", http.StatusInternalServerError)
				return
			}
		}
		writeJSON(w, http.StatusOK, a.response(service))
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
		id, err := strconv.Atoi(idText)
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
	if err := a.cfg.WriteService(service); err != nil {
		http.Error(w, "failed to update service", http.StatusInternalServerError)
		return
	}
	if !service.Paused {
		if err := a.tracker.Schedule(service); err != nil {
			http.Error(w, "service updated, but tracking could not be restarted", http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, http.StatusOK, a.response(service))
}

func (a *serviceAPI) delete(w http.ResponseWriter, id int) {
	if err := a.cfg.DeleteService(id); err != nil {
		http.Error(w, "service not found", http.StatusNotFound)
		return
	}
	a.tracker.Unschedule(id)
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
	end := time.Now().UTC()
	start := end.Add(-5 * time.Minute)
	if _, err := collection.QueryPrometheusRange(ctx, a.httpClient, request.PrometheusURL, request.LoadQuery, start, end); err != nil {
		http.Error(w, "load query failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	if _, err := collection.QueryPrometheusRange(ctx, a.httpClient, request.PrometheusURL, request.LatencyQuery, start, end); err != nil {
		http.Error(w, "latency query failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Connection and queries succeeded"})
}
