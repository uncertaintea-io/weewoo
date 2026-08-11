package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/config"
)

type settingsRoundTripFunc func(*http.Request) (*http.Response, error)

func (f settingsRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestSettingsAPIGuidesFirstRunAndPersistsConfiguration(t *testing.T) {
	cfg := config.NewFakeConfig()
	handler := NewSettingsAPIHandler(cfg)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	require.Equal(t, http.StatusOK, first.Code)
	var initial applicationSettings
	require.NoError(t, json.NewDecoder(first.Body).Decode(&initial))
	assert.False(t, initial.SetupComplete)

	body := bytes.NewBufferString(`{"alertmanagerUrl":"https://alerts.example.com:9093/"}`)
	saved := httptest.NewRecorder()
	handler.ServeHTTP(saved, httptest.NewRequest(http.MethodPut, "/api/settings", body))
	require.Equal(t, http.StatusOK, saved.Code, saved.Body.String())
	assert.Equal(t, "alerts.example.com:9093", mustConfig(t, cfg, config.AlertmanagerHostConfigKey))
	assert.Equal(t, "https://alerts.example.com:9093", mustConfig(t, cfg, config.AlertmanagerURLConfigKey))
	assert.Equal(t, "true", mustConfig(t, cfg, config.SetupCompleteConfigKey))
}

func TestSettingsAPIRejectsInvalidURLsWithoutCompletingSetup(t *testing.T) {
	cfg := config.NewFakeConfig()
	handler := NewSettingsAPIHandler(cfg)
	body := bytes.NewBufferString(`{"alertmanagerUrl":"alerts:9093"}`)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/settings", body))
	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Empty(t, mustConfig(t, cfg, config.SetupCompleteConfigKey))
}

func TestSettingsAPITreatsLegacyAlertmanagerConfigurationAsComplete(t *testing.T) {
	cfg := config.NewFakeConfig()
	require.NoError(t, cfg.SetConfig(config.AlertmanagerHostConfigKey, "alerts.internal:9093"))
	handler := NewSettingsAPIHandler(cfg)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/settings", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	var settings applicationSettings
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&settings))
	assert.True(t, settings.SetupComplete)
	assert.Equal(t, "http://alerts.internal:9093", settings.AlertmanagerURL)
}

func TestSettingsAPITestsAlertmanagerReadiness(t *testing.T) {
	client := &http.Client{Transport: settingsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "/-/ready", r.URL.Path)
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader("ready"))}, nil
	})}
	handler := NewSettingsAPIHandler(config.NewFakeConfig(), client)
	recorder := httptest.NewRecorder()
	body := strings.NewReader(`{"alertmanagerUrl":"http://alerts:9093/"}`)

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/settings/test", body))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.JSONEq(t, `{"ready":true,"message":"Alertmanager is ready."}`, recorder.Body.String())
}

func TestSettingsAPIReturnsBadGatewayWhenAlertmanagerIsNotReady(t *testing.T) {
	client := &http.Client{Transport: settingsRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable", Body: io.NopCloser(strings.NewReader("not ready"))}, nil
	})}
	handler := NewSettingsAPIHandler(config.NewFakeConfig(), client)
	recorder := httptest.NewRecorder()
	body := strings.NewReader(`{"alertmanagerUrl":"http://alerts:9093"}`)

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/settings/test", body))

	assert.Equal(t, http.StatusBadGateway, recorder.Code)
}

func mustConfig(t *testing.T, cfg config.Config, key string) string {
	t.Helper()
	value, err := cfg.GetConfig(key)
	require.NoError(t, err)
	return value
}
