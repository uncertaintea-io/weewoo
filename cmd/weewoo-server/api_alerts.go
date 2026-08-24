// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/uncertaintea-io/weewoo/internal/alerting"
)

type alertAPI struct {
	manager alertReaderReviewer
}

type alertReaderReviewer interface {
	List(context.Context, bool, int) ([]alerting.Alert, error)
	GetEvidence(context.Context, int64) (alerting.AlertEvidence, error)
	ReviewOccurrence(context.Context, int64, int64, bool, string) (alerting.ReviewResult, error)
}

type reviewOccurrenceRequest struct {
	Revision int64  `json:"revision"`
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason"`
}

func NewAlertAPIHandler(manager alertReaderReviewer) http.Handler {
	return &alertAPI{manager: manager}
}

func (a *alertAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a.manager == nil {
		http.Error(w, "alerts are unavailable", http.StatusServiceUnavailable)
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/alerts"), "/")
	if path == "" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		includeResolved := r.URL.Query().Get("history") != "false"
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		alerts, err := a.manager.List(r.Context(), includeResolved, limit)
		if err != nil {
			http.Error(w, "failed to read alerts", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, alerts)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 3 && parts[0] == "occurrences" && parts[2] == "review" {
		a.review(w, r, parts[1])
		return
	}
	if len(parts) == 3 && parts[0] == "occurrences" && parts[2] == "evidence" {
		a.evidence(w, r, parts[1])
		return
	}
	http.NotFound(w, r)
}

func (a *alertAPI) evidence(w http.ResponseWriter, r *http.Request, idText string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid occurrence ID", http.StatusBadRequest)
		return
	}
	result, err := a.manager.GetEvidence(r.Context(), id)
	if errors.Is(err, alerting.ErrOccurrenceNotFound) {
		http.Error(w, "alert occurrence not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, alerting.ErrEvidenceNotApplicable) {
		http.Error(w, "alert evidence is only available for anomaly occurrences", http.StatusUnprocessableEntity)
		return
	}
	if errors.Is(err, alerting.ErrEvidenceReferenceGone) {
		http.Error(w, "the matching reference distribution is no longer retained", http.StatusGone)
		return
	}
	if err != nil {
		http.Error(w, "failed to read alert evidence", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *alertAPI) review(w http.ResponseWriter, r *http.Request, idText string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "cross-origin review requests are not allowed", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid occurrence ID", http.StatusBadRequest)
		return
	}
	var request reviewOccurrenceRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Revision < 0 {
		http.Error(w, "invalid review request", http.StatusBadRequest)
		return
	}
	result, err := a.manager.ReviewOccurrence(r.Context(), id, request.Revision, request.Accepted, request.Reason)
	if errors.Is(err, alerting.ErrReviewConflict) {
		http.Error(w, "this occurrence was reviewed by another request; refresh and try again", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "failed to review occurrence", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return parsed.Host == r.Host
}
