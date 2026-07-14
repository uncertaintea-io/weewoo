package alerting

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/config"
)

type testConfig struct {
	config.Config
	alertManagerURL string
}

func (c testConfig) AlertManagerURL() string {
	return c.alertManagerURL
}

func TestSendIt(t *testing.T) {
	type requestDetails struct {
		method string
		path   string
	}

	requests := make(chan requestDetails, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- requestDetails{method: r.Method, path: r.URL.Path}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	cfg := config.NewFakeConfig()
	cfg.SetConfig("alertmanager_host", server.Listener.Addr().String())

	err := SendIt(cfg, AlertingOptions{
		Service:     "test",
		Indicator:   "test",
		AlertName:   "test",
		Summary:     "test",
		Description: "test",
		Annotations: map[string]string{},
		Serverity:   "critical",
	})
	require.NoError(t, err)

	request := <-requests
	require.Equal(t, http.MethodPost, request.method)
	require.Equal(t, "/api/v2/alerts", request.path)
}
