package alerting

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/config"
)

type testConfig struct {
	config.Config
	alertManagerURL string
}

func TestSendItContextHonorsDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	cfg := config.NewFakeConfig()
	require.NoError(t, cfg.SetConfig("alertmanager_host", server.Listener.Addr().String()))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := SendItContext(ctx, cfg, AlertingOptions{AlertName: "test"})
	require.Error(t, err)
	require.True(t, errors.Is(err, context.DeadlineExceeded), "expected deadline error, got %v", err)
	require.Less(t, time.Since(start), 200*time.Millisecond)
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
