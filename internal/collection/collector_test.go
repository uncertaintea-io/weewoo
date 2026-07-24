package collection

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

type unavailableJointStore struct{}

func (unavailableJointStore) Publish(context.Context, int, int, time.Time, func(io.Writer) error) (int64, bool, error) {
	return 0, false, errors.New("unavailable")
}

func (unavailableJointStore) ReadCurrent(context.Context, int, int) ([]byte, error) {
	return nil, errors.New("unavailable")
}

func TestCollectionSucceedsWhenAnalysisFails(t *testing.T) {
	prometheus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status":"success",
			"data":{"result":[{"values":[[1710000000,"1"]]}]}
		}`))
	}))
	t.Cleanup(prometheus.Close)

	service := &config.Service{
		Id:            1,
		Name:          "checkout",
		PrometheusURL: prometheus.URL,
		LoadQuery:     "load",
		LatencyQuery:  "latency",
		Interval:      time.Minute,
	}
	analysisWorker := NewAnalysisWorker(config.NewFakeConfig(), unavailableJointStore{}, nil, 1)
	t.Cleanup(analysisWorker.Stop)
	collector := &collector{
		client:     prometheus.Client(),
		chunkStore: ecdf.NewFakeChunkStore(),
		analyzer:   analysisWorker,
	}

	err := collector.collectSamples(context.Background(), service, time.Unix(1_710_000_000, 0), time.Unix(1_710_000_060, 0))

	require.NoError(t, err)
}
