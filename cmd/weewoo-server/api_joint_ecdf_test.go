package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

const (
	jointServiceID   = 7
	jointIndicatorID = 9
	okPath           = "/api/jecdf?serviceId=7&indicatorId=9&options=2"
	fakeJointECDF    = "test"
)

func testJointStore() ecdf.JointStore {
	store := ecdf.NewFakeJointStore()
	store.Publish(context.Background(), jointServiceID, jointIndicatorID, time.Now(), ecdf.BuildFakeJointECDF([]byte(fakeJointECDF)))
	return store
}

func TestJointECDFAPIReadsAndRendersCurrentECDF(t *testing.T) {
	var renderBody []byte
	handler := &jointECDFAPI{
		store: testJointStore(),
		render: func(_ context.Context, body []byte, width, height int, options ecdf.RenderOptions) (*ecdf.RenderResponse, error) {
			renderBody = body
			assert.Equal(t, jointECDFRenderWidth, width)
			assert.Equal(t, jointECDFRenderHeight, height)
			assert.Equal(t, ecdf.RenderOptionLogY, options)
			return &ecdf.RenderResponse{
				Width: width, Height: height,
				XMin: 1, XMax: 2, YMin: 3, YMax: 4,
				Masses: []float64{0.25},
			}, nil
		},
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, okPath, nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	assert.Equal(t, fakeJointECDF, string(renderBody))
	assert.JSONEq(t, `{
		"width":128, "height":128,
		"xMin":1, "xMax":2, "yMin":3, "yMax":4,
		"masses":[0.25]
	}`, recorder.Body.String())
}

func TestJointECDFAPIValidatesRequest(t *testing.T) {
	store := testJointStore()

	tests := []struct {
		name   string
		method string
		target string
		status int
	}{
		{name: "method", method: http.MethodPost, target: "/api/jecdf?serviceId=1&indicatorId=2", status: http.StatusMethodNotAllowed},
		{name: "missing service", method: http.MethodGet, target: "/api/jecdf?indicatorId=2", status: http.StatusBadRequest},
		{name: "invalid service", method: http.MethodGet, target: "/api/jecdf?serviceId=nope&indicatorId=2", status: http.StatusBadRequest},
		{name: "missing indicator", method: http.MethodGet, target: "/api/jecdf?serviceId=1", status: http.StatusBadRequest},
		{name: "invalid indicator", method: http.MethodGet, target: "/api/jecdf?serviceId=1&indicatorId=0", status: http.StatusBadRequest},
		{name: "invalid options", method: http.MethodGet, target: "/api/jecdf?serviceId=1&indicatorId=2&options=nope", status: http.StatusBadRequest},
		{name: "unsupported options", method: http.MethodGet, target: "/api/jecdf?serviceId=1&indicatorId=2&options=4", status: http.StatusBadRequest},
		{name: "unknown child path", method: http.MethodGet, target: "/api/jecdf/other?serviceId=1&indicatorId=2", status: http.StatusNotFound},
		{name: "not found", method: http.MethodGet, target: "/api/jecdf?serviceId=1&indicatorId=2", status: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			NewJointECDFAPIHandler(store).ServeHTTP(
				recorder,
				httptest.NewRequest(tt.method, tt.target, nil),
			)
			assert.Equal(t, tt.status, recorder.Code)
		})
	}
}

func TestJointECDFAPIReportsRenderFailure(t *testing.T) {
	recorder := httptest.NewRecorder()
	handler := &jointECDFAPI{
		store: testJointStore(),
		render: func(context.Context, []byte, int, int, ecdf.RenderOptions) (*ecdf.RenderResponse, error) {
			return nil, errors.New("render failed")
		},
	}
	handler.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, okPath, nil),
	)
	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.Equal(t, "failed to render joint ECDF\n", recorder.Body.String())
}
