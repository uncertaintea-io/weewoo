package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/alerting"
)

type fakeAlertManager struct {
	alerts         []alerting.Alert
	includeHistory bool
	limit          int
	reviewedID     int64
	revision       int64
	accepted       bool
	reason         string
	reviewErr      error
	cdf            alerting.CDFDetails
	cdfID          int64
	cdfErr         error
}

func (f *fakeAlertManager) GetOccurrenceCDF(_ context.Context, id int64) (alerting.CDFDetails, error) {
	f.cdfID = id
	return f.cdf, f.cdfErr
}

func (f *fakeAlertManager) List(_ context.Context, history bool, limit int) ([]alerting.Alert, error) {
	f.includeHistory, f.limit = history, limit
	return f.alerts, nil
}

func (f *fakeAlertManager) ReviewOccurrence(_ context.Context, id, revision int64, accepted bool, reason string) (alerting.ReviewResult, error) {
	f.reviewedID, f.revision, f.accepted, f.reason = id, revision, accepted, reason
	return alerting.ReviewResult{OccurrenceID: id, Revision: revision + 1, Accepted: accepted, ReviewedAt: time.Unix(10, 0)}, f.reviewErr
}

func TestAlertAPIListsHistory(t *testing.T) {
	manager := &fakeAlertManager{alerts: []alerting.Alert{{ID: 7, Title: "Anomaly"}}}
	recorder := httptest.NewRecorder()

	NewAlertAPIHandler(manager).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/alerts?history=true&limit=25", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.True(t, manager.includeHistory)
	assert.Equal(t, 25, manager.limit)
	var response []alerting.Alert
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
	require.Len(t, response, 1)
	assert.Equal(t, int64(7), response[0].ID)
}

func TestAlertAPIReviewsOccurrence(t *testing.T) {
	manager := &fakeAlertManager{}
	body := bytes.NewBufferString(`{"revision":2,"accepted":true,"reason":"planned deployment"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/alerts/occurrences/9/review", body)
	request.Host = "weewoo.example"
	request.Header.Set("Origin", "https://weewoo.example")
	recorder := httptest.NewRecorder()

	NewAlertAPIHandler(manager).ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, int64(9), manager.reviewedID)
	assert.Equal(t, int64(2), manager.revision)
	assert.True(t, manager.accepted)
	assert.Equal(t, "planned deployment", manager.reason)
}

func TestAlertAPIReturnsOccurrenceCDFDetails(t *testing.T) {
	manager := &fakeAlertManager{cdf: alerting.CDFDetails{
		Query:   alerting.CDFQuery{Input: 12, Xs: []float64{30, 40}, Ps: []float64{0.25, 0.75}},
		Samples: []alerting.CDFSample{{Value: 34, Count: 5}},
		PValue:  0.001,
	}}
	recorder := httptest.NewRecorder()

	NewAlertAPIHandler(manager).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/alerts/occurrences/9/cdf", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, int64(9), manager.cdfID)
	var response alerting.CDFDetails
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
	assert.Equal(t, 12.0, response.Query.Input)
	assert.Equal(t, []float64{30, 40}, response.Query.Xs)
	assert.Equal(t, []float64{0.25, 0.75}, response.Query.Ps)
	assert.Equal(t, []alerting.CDFSample{{Value: 34, Count: 5}}, response.Samples)
	assert.Equal(t, 0.001, response.PValue)
}

func TestAlertAPIRejectsCDFForNonAnomalyOccurrence(t *testing.T) {
	manager := &fakeAlertManager{cdfErr: alerting.ErrCDFNotApplicable}
	recorder := httptest.NewRecorder()

	NewAlertAPIHandler(manager).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/alerts/occurrences/9/cdf", nil))

	assert.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
}

func TestAlertAPIReportsExpiredCDFReference(t *testing.T) {
	manager := &fakeAlertManager{cdfErr: alerting.ErrCDFReferenceGone}
	recorder := httptest.NewRecorder()

	NewAlertAPIHandler(manager).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/alerts/occurrences/9/cdf", nil))

	assert.Equal(t, http.StatusGone, recorder.Code)
	assert.Equal(t, "the matching reference distribution is no longer retained\n", recorder.Body.String())
}

func TestAlertAPIRejectsCrossOriginReview(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/alerts/occurrences/9/review", bytes.NewBufferString(`{"revision":0,"accepted":true}`))
	request.Host = "weewoo.example"
	request.Header.Set("Origin", "https://attacker.example")
	recorder := httptest.NewRecorder()

	NewAlertAPIHandler(&fakeAlertManager{}).ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestAlertAPIReportsReviewConflict(t *testing.T) {
	manager := &fakeAlertManager{reviewErr: alerting.ErrReviewConflict}
	recorder := httptest.NewRecorder()

	NewAlertAPIHandler(manager).ServeHTTP(recorder, httptest.NewRequest(
		http.MethodPost,
		"/api/alerts/occurrences/9/review",
		bytes.NewBufferString(`{"revision":1,"accepted":false}`),
	))

	assert.Equal(t, http.StatusConflict, recorder.Code)
}
