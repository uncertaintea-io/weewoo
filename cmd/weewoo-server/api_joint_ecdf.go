package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

const (
	jointECDFRenderWidth  = 128
	jointECDFRenderHeight = 128
)

type jointECDFRenderer func(ctx context.Context, jointECDF []byte, width, height int) (*ecdf.RenderResponse, error)

type jointECDFAPI struct {
	store  ecdf.JointStore
	render jointECDFRenderer
}

func NewJointECDFAPIHandler(store ecdf.JointStore) http.Handler {
	return &jointECDFAPI{store: store, render: ecdf.Render}
}

func (a *jointECDFAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/jecdf" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if a.store == nil {
		http.Error(w, "joint ECDF data is unavailable", http.StatusServiceUnavailable)
		return
	}

	serviceID, err := positiveQueryID(r, "serviceId")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	indicatorID, err := positiveQueryID(r, "indicatorId")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	jointECDF, err := a.store.ReadCurrent(r.Context(), serviceID, indicatorID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "joint ECDF not found", http.StatusNotFound)
			return
		}
		slog.Error("failed to read joint ECDF", "service_id", serviceID, "indicator_id", indicatorID, "error", err)
		http.Error(w, "failed to read joint ECDF", http.StatusInternalServerError)
		return
	}

	response, err := a.render(r.Context(), jointECDF, jointECDFRenderWidth, jointECDFRenderHeight)
	if err != nil {
		slog.Error("failed to render joint ECDF", "service_id", serviceID, "indicator_id", indicatorID, "error", err)
		http.Error(w, "failed to render joint ECDF", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func positiveQueryID(r *http.Request, name string) (int, error) {
	value := r.URL.Query().Get(name)
	id, err := strconv.Atoi(value)
	if err != nil || id <= 0 {
		return 0, errors.New(name + " must be a positive integer")
	}
	return id, nil
}
