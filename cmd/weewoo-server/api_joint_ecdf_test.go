// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

func testJointHandler(render jointECDFRenderer) *jointECDFAPI {
	return &jointECDFAPI{
		store: testJointStore(),
		render: newJointECDFRenderCoordinator(
			render, jointECDFRenderConcurrency, jointECDFRenderTimeout, jointECDFRenderCacheBytes,
		),
	}
}

func TestJointECDFAPIReadsAndRendersCurrentECDF(t *testing.T) {
	var renderBody []byte
	handler := testJointHandler(
		func(_ context.Context, body []byte, width, height int, options ecdf.RenderOptions) (*ecdf.RenderResponse, error) {
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
	)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, okPath, nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	assert.NotEmpty(t, recorder.Header().Get("ETag"))
	assert.Equal(t, "private, no-cache", recorder.Header().Get("Cache-Control"))
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
	handler := testJointHandler(
		func(context.Context, []byte, int, int, ecdf.RenderOptions) (*ecdf.RenderResponse, error) {
			return nil, errors.New("render failed")
		},
	)
	handler.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, okPath, nil),
	)
	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.Equal(t, "failed to render joint ECDF\n", recorder.Body.String())
}

func TestJointECDFAPIRejectsRenderWhenAtCapacity(t *testing.T) {
	handler := testJointHandler(
		func(_ context.Context, _ []byte, width, height int, _ ecdf.RenderOptions) (*ecdf.RenderResponse, error) {
			return successfulJointRender(width, height), nil
		},
	)
	for range cap(handler.render.slots) {
		handler.render.slots <- struct{}{}
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, okPath, nil))

	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.Equal(t, "1", recorder.Header().Get("Retry-After"))
	assert.Equal(t, "joint ECDF renderer is busy\n", recorder.Body.String())
}

func TestJointECDFAPIReportsRenderTimeout(t *testing.T) {
	handler := testJointHandler(
		func(ctx context.Context, _ []byte, _, _ int, _ ecdf.RenderOptions) (*ecdf.RenderResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	)
	handler.render.timeout = time.Millisecond

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, okPath, nil))

	assert.Equal(t, http.StatusGatewayTimeout, recorder.Code)
	assert.Equal(t, "joint ECDF render timed out\n", recorder.Body.String())
}

func TestJointECDFAPIReturnsNotModifiedWithoutRendering(t *testing.T) {
	var calls atomic.Int32
	handler := testJointHandler(
		func(_ context.Context, _ []byte, width, height int, _ ecdf.RenderOptions) (*ecdf.RenderResponse, error) {
			calls.Add(1)
			return &ecdf.RenderResponse{Width: width, Height: height, Masses: []float64{}}, nil
		},
	)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, okPath, nil))
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, int32(1), calls.Load())
	etag := first.Header().Get("ETag")
	require.NotEmpty(t, etag)

	request := httptest.NewRequest(http.MethodGet, okPath, nil)
	request.Header.Set("If-None-Match", `"other", W/`+etag)
	notModified := httptest.NewRecorder()
	handler.ServeHTTP(notModified, request)

	assert.Equal(t, http.StatusNotModified, notModified.Code)
	assert.Empty(t, notModified.Body.String())
	assert.Equal(t, etag, notModified.Header().Get("ETag"))
	assert.Equal(t, "private, no-cache", notModified.Header().Get("Cache-Control"))
	assert.Equal(t, int32(1), calls.Load())
}

func TestJointECDFRenderETagIncludesRepresentationInputs(t *testing.T) {
	base := jointECDFRenderETag("definition", 128, 128, 0)
	assert.Equal(t, base, jointECDFRenderETag("definition", 128, 128, 0))
	assert.NotEqual(t, base, jointECDFRenderETag("changed", 128, 128, 0))
	assert.NotEqual(t, base, jointECDFRenderETag("definition", 64, 128, 0))
	assert.NotEqual(t, base, jointECDFRenderETag("definition", 128, 128, ecdf.RenderOptionLogY))
}

func TestJointECDFRenderTimeoutFitsServerWriteTimeout(t *testing.T) {
	assert.Equal(t, 15*time.Second, jointECDFRenderTimeout)
	assert.Less(t, jointECDFRenderTimeout, appServerWriteTimeout)
}
