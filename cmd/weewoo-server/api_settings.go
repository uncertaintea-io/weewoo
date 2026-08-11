package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/config"
)

const (
	alertmanagerTestTimeout = 5 * time.Second
)

type applicationSettings struct {
	AlertmanagerURL string `json:"alertmanagerUrl"`
	SetupComplete   bool   `json:"setupComplete"`
}

type settingsAPI struct {
	cfg        config.Config
	httpClient *http.Client
}

func NewSettingsAPIHandler(cfg config.Config, clients ...*http.Client) http.Handler {
	client := http.DefaultClient
	if len(clients) > 0 && clients[0] != nil {
		client = clients[0]
	}
	return &settingsAPI{cfg: cfg, httpClient: client}
}

func (a *settingsAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/test") {
		a.testConnection(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		settings, err := a.read()
		if err != nil {
			http.Error(w, "failed to read settings", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, settings)
	case http.MethodPut:
		request, ok := decodeSettingsRequest(w, r)
		if !ok {
			return
		}
		alertmanagerURL, err := validateHTTPURL("alertmanagerUrl", request.AlertmanagerURL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		parsedAlertmanager, _ := url.Parse(alertmanagerURL)
		if err := a.cfg.SetConfigs(map[string]string{
			config.AlertmanagerURLConfigKey:  alertmanagerURL,
			config.AlertmanagerHostConfigKey: parsedAlertmanager.Host,
			config.SetupCompleteConfigKey:    "true",
		}); err != nil {
			http.Error(w, "failed to save settings", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, applicationSettings{AlertmanagerURL: alertmanagerURL, SetupComplete: true})
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPut)
	}
}

func decodeSettingsRequest(w http.ResponseWriter, r *http.Request) (applicationSettings, bool) {
	var request applicationSettings
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid JSON request", http.StatusBadRequest)
		return applicationSettings{}, false
	}
	return request, true
}

func (a *settingsAPI) read() (applicationSettings, error) {
	alertmanagerHost, err := a.cfg.GetConfig(config.AlertmanagerHostConfigKey)
	if err != nil {
		return applicationSettings{}, err
	}
	complete, err := a.cfg.GetConfig(config.SetupCompleteConfigKey)
	if err != nil {
		return applicationSettings{}, err
	}
	alertmanagerURL, err := a.cfg.GetConfig(config.AlertmanagerURLConfigKey)
	if err != nil {
		return applicationSettings{}, err
	}
	if alertmanagerURL == "" {
		alertmanagerURL = alertmanagerHost
	}
	if alertmanagerURL != "" && !strings.Contains(alertmanagerURL, "://") {
		alertmanagerURL = "http://" + alertmanagerURL
	}
	// Existing installations configured before onboarding already count as set up.
	setupComplete := complete == "true" || alertmanagerHost != ""
	return applicationSettings{AlertmanagerURL: alertmanagerURL, SetupComplete: setupComplete}, nil
}

func (a *settingsAPI) testConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	request, ok := decodeSettingsRequest(w, r)
	if !ok {
		return
	}
	baseURL, err := validateHTTPURL("alertmanagerUrl", request.AlertmanagerURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), alertmanagerTestTimeout)
	defer cancel()
	probe, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/-/ready", nil)
	if err != nil {
		http.Error(w, "failed to create Alertmanager readiness request", http.StatusBadRequest)
		return
	}
	response, err := a.httpClient.Do(probe)
	if err != nil {
		http.Error(w, "could not connect to Alertmanager: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		http.Error(w, fmt.Sprintf("Alertmanager readiness check returned %s", response.Status), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ready": true, "message": "Alertmanager is ready."})
}

func validateHTTPURL(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("%s must be an HTTP or HTTPS URL", field)
	}
	return strings.TrimRight(value, "/"), nil
}
