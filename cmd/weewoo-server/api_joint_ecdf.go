package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

const (
	jointECDFRenderWidth  = 128
	jointECDFRenderHeight = 128
	jointECDFRenderFormat = 1
)

type jointECDFRenderer func(
	ctx context.Context,
	jointECDF []byte,
	width, height int,
	options ecdf.RenderOptions,
) (*ecdf.RenderResponse, error)

type jointECDFAPI struct {
	store  ecdf.JointStore
	render *jointECDFRenderCoordinator
}

func NewJointECDFAPIHandler(store ecdf.JointStore) http.Handler {
	return &jointECDFAPI{
		store: store,
		render: newJointECDFRenderCoordinator(
			ecdf.Render,
			jointECDFRenderConcurrency,
			jointECDFRenderTimeout,
			jointECDFRenderCacheBytes,
		),
	}
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
	options, err := renderOptionsQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	body, sha, err := a.store.ReadCurrent(r.Context(), serviceID, indicatorID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "joint ECDF not found", http.StatusNotFound)
			return
		}
		slog.Error("failed to read joint ECDF", "service_id", serviceID, "indicator_id", indicatorID, "error", err)
		http.Error(w, "failed to read joint ECDF", http.StatusInternalServerError)
		return
	}

	etag := jointECDFRenderETag(sha, jointECDFRenderWidth, jointECDFRenderHeight, options)
	w.Header().Set("Cache-Control", "private, no-cache")
	if ifNoneMatch(r.Header.Get("If-None-Match"), etag) {
		w.Header().Set("ETag", etag)
		jointECDFRenderEvents.WithLabelValues("not_modified").Inc()
		w.WriteHeader(http.StatusNotModified)
		return
	}

	response, err := a.render.Render(
		r.Context(), etag, body,
		jointECDFRenderWidth, jointECDFRenderHeight, options,
	)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		slog.Error("failed to render joint ECDF", "service_id", serviceID, "indicator_id", indicatorID, "error", err)
		if errors.Is(err, errJointECDFRenderBusy) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "joint ECDF renderer is busy", http.StatusServiceUnavailable)
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			http.Error(w, "joint ECDF render timed out", http.StatusGatewayTimeout)
			return
		}
		http.Error(w, "failed to render joint ECDF", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(response)
}

func jointECDFRenderETag(definitionHash string, width, height int, options ecdf.RenderOptions) string {
	representation := fmt.Sprintf(
		"definition=%s;width=%d;height=%d;options=%d;format=%d",
		definitionHash, width, height, options, jointECDFRenderFormat,
	)
	sum := sha256.Sum256([]byte(representation))
	return fmt.Sprintf("\"%x\"", sum)
}

func ifNoneMatch(header, current string) bool {
	for candidate := range strings.SplitSeq(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == current {
			return true
		}
	}
	return false
}

func renderOptionsQuery(r *http.Request) (ecdf.RenderOptions, error) {
	value := r.URL.Query().Get("options")
	if value == "" {
		return 0, nil
	}
	options, err := strconv.Atoi(value)
	if err != nil || options < 0 || ecdf.RenderOptions(options)&^ecdf.AllRenderOptions != 0 {
		return 0, errors.New("options must be an integer between 0 and 3")
	}
	return ecdf.RenderOptions(options), nil
}

func positiveQueryID(r *http.Request, name string) (int, error) {
	value := r.URL.Query().Get(name)
	id, err := strconv.Atoi(value)
	if err != nil || id <= 0 {
		return 0, errors.New(name + " must be a positive integer")
	}
	return id, nil
}
