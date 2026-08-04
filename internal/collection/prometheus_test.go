// Prometheus golden responses can be regenerated against a live server with:
//
//	go test ./internal/collection -run '^TestUpdatePrometheusGoldens$' -count=1 -args -update-prometheus-goldens -prometheus-url=http://your-server:9090
package collection

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

const (
	liveLatencyQuery = `sum by (app) (weewoo_http_request_duration_seconds{app="weewoo"})`
	liveLoadQuery    = `irate(histogram_count(sum by (app) (weewoo_http_request_duration_seconds{app="weewoo"}))[30s:])`

	prometheusGoldenBaseURL = "http://prometheus.invalid"
	prometheusGoldenStep    = time.Minute
)

var (
	updatePrometheusGoldens = flag.Bool("update-prometheus-goldens", false, "regenerate Prometheus golden responses")
	prometheusURL           = flag.String("prometheus-url", "", "live Prometheus server used to regenerate golden responses")
)

type prometheusGolden struct {
	Query        string            `json:"query"`
	Start        time.Time         `json:"start"`
	End          time.Time         `json:"end"`
	StepSeconds  int64             `json:"step_seconds"`
	StatusCode   int               `json:"status_code"`
	ContentType  string            `json:"content_type"`
	ResponseBody json.RawMessage   `json:"response_body"`
	WantSamples  []ecdf.Sample     `json:"want_samples,omitempty"`
	WantPoints   []PrometheusPoint `json:"want_points,omitempty"`
}

func TestPrometheusLatencySamplesGolden(t *testing.T) {
	golden := loadPrometheusGolden(t, "latency-range.json", liveLatencyQuery)

	samples, err := QueryPrometheusRangeSamples(
		context.Background(),
		golden.httpClient(t),
		prometheusGoldenBaseURL,
		liveLatencyQuery,
		golden.Start,
		golden.End,
		golden.step(),
	)

	require.NoError(t, err)
	assert.Equal(t, golden.WantSamples, samples)
	assertSamplesValid(t, samples)
}

func TestPrometheusLoadSamplesGolden(t *testing.T) {
	golden := loadPrometheusGolden(t, "load-range.json", liveLoadQuery)

	samples, err := QueryPrometheusRangeSamples(
		context.Background(),
		golden.httpClient(t),
		prometheusGoldenBaseURL,
		liveLoadQuery,
		golden.Start,
		golden.End,
		golden.step(),
	)

	require.NoError(t, err)
	assert.Equal(t, golden.WantSamples, samples)
	assertSamplesValid(t, samples)
}

func TestPrometheusLoadPointsGolden(t *testing.T) {
	golden := loadPrometheusGolden(t, "load-range.json", liveLoadQuery)

	points, err := QueryPrometheusRangePoints(
		context.Background(),
		golden.httpClient(t),
		prometheusGoldenBaseURL,
		liveLoadQuery,
		golden.Start,
		golden.End,
		golden.step(),
	)

	require.NoError(t, err)
	assertPrometheusPointsEqual(t, golden.WantPoints, points)
	require.NotEmpty(t, points)
	assert.True(t, slices.IsSortedFunc(points, func(a, b PrometheusPoint) int {
		return a.Timestamp.Compare(b.Timestamp)
	}), "points are not sorted by timestamp")
	assert.True(t, points[0].Timestamp.After(golden.Start), "first point overlaps with previous window")
}

func TestUpdatePrometheusGoldens(t *testing.T) {
	if !*updatePrometheusGoldens {
		t.Skip("pass -update-prometheus-goldens to regenerate Prometheus golden responses")
	}
	require.NotEmpty(t, *prometheusURL, "-prometheus-url is required when updating Prometheus golden responses")

	end := time.Now().UTC().Truncate(time.Minute)
	start := end.Add(-5 * time.Minute)

	t.Run("latency", func(t *testing.T) {
		recorder := &prometheusResponseRecorder{transport: http.DefaultTransport}
		samples, err := QueryPrometheusRangeSamples(
			context.Background(),
			&http.Client{Transport: recorder},
			*prometheusURL,
			liveLatencyQuery,
			start,
			end,
			prometheusGoldenStep,
		)
		require.NoError(t, err)

		writePrometheusGolden(t, "latency-range.json", recorder.golden(liveLatencyQuery, start, end, samples, nil))
	})

	t.Run("load", func(t *testing.T) {
		recorder := &prometheusResponseRecorder{transport: http.DefaultTransport}
		points, err := QueryPrometheusRangePoints(
			context.Background(),
			&http.Client{Transport: recorder},
			*prometheusURL,
			liveLoadQuery,
			start,
			end,
			prometheusGoldenStep,
		)
		require.NoError(t, err)

		golden := recorder.golden(liveLoadQuery, start, end, nil, points)
		samples, err := QueryPrometheusRangeSamples(
			context.Background(),
			golden.httpClient(t),
			prometheusGoldenBaseURL,
			liveLoadQuery,
			start,
			end,
			prometheusGoldenStep,
		)
		require.NoError(t, err)
		golden.WantSamples = samples

		writePrometheusGolden(t, "load-range.json", golden)
	})
}

func assertSamplesValid(t *testing.T, samples []ecdf.Sample) {
	t.Helper()
	require.NotEmpty(t, samples)
	assert.True(t, slices.IsSortedFunc(samples, func(a, b ecdf.Sample) int {
		return cmp.Compare(a.Value, b.Value)
	}), "samples are not sorted by value")
	for _, sample := range samples {
		assert.Greater(t, sample.Count, uint64(0), "value %g has a zero count", sample.Value)
	}
}

func assertPrometheusPointsEqual(t *testing.T, want, got []PrometheusPoint) {
	t.Helper()
	require.Len(t, got, len(want))
	for i := range want {
		assert.True(t, want[i].Timestamp.Equal(got[i].Timestamp), "point %d timestamp: want %v, got %v", i, want[i].Timestamp, got[i].Timestamp)
		assert.Equal(t, want[i].Value, got[i].Value, "point %d value", i)
	}
}

func loadPrometheusGolden(t *testing.T, name, query string) prometheusGolden {
	t.Helper()

	data, err := os.ReadFile(prometheusGoldenPath(name))
	require.NoError(t, err)

	var golden prometheusGolden
	require.NoError(t, json.Unmarshal(data, &golden))
	require.Equal(t, query, golden.Query, "golden query is stale; regenerate the Prometheus golden responses")
	require.True(t, golden.End.After(golden.Start), "golden response has an invalid time range")
	require.Positive(t, golden.StepSeconds, "golden response has an invalid step")
	require.True(t, json.Valid(golden.ResponseBody), "golden response body is not valid JSON")
	return golden
}

func writePrometheusGolden(t *testing.T, name string, golden prometheusGolden) {
	t.Helper()

	path := prometheusGoldenPath(name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	require.NoError(t, err)
	temporaryName := temporary.Name()
	t.Cleanup(func() {
		_ = os.Remove(temporaryName)
	})

	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	require.NoError(t, encoder.Encode(golden))
	require.NoError(t, temporary.Chmod(0o644))
	require.NoError(t, temporary.Close())
	require.NoError(t, os.Rename(temporaryName, path))
}

func prometheusGoldenPath(name string) string {
	return filepath.Join("testdata", "prometheus", name)
}

func (g prometheusGolden) step() time.Duration {
	return time.Duration(g.StepSeconds) * time.Second
}

func (g prometheusGolden) httpClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, request.Method)
		require.Equal(t, "/api/v1/query_range", request.URL.Path)
		require.NoError(t, request.ParseForm())
		require.Equal(t, g.Query, request.Form.Get("query"))
		require.Equal(t, g.Start, prometheusRequestTime(t, request, "start"))
		require.Equal(t, g.End, prometheusRequestTime(t, request, "end"))

		stepSeconds, err := strconv.ParseFloat(request.Form.Get("step"), 64)
		require.NoError(t, err)
		require.Equal(t, float64(g.StepSeconds), stepSeconds)

		return &http.Response{
			StatusCode: g.StatusCode,
			Status:     fmt.Sprintf("%d %s", g.StatusCode, http.StatusText(g.StatusCode)),
			Header:     http.Header{"Content-Type": {g.ContentType}},
			Body:       io.NopCloser(bytes.NewReader(g.ResponseBody)),
			Request:    request,
		}, nil
	})}
}

type prometheusResponseRecorder struct {
	transport    http.RoundTripper
	statusCode   int
	contentType  string
	responseBody []byte
}

func (r *prometheusResponseRecorder) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := r.transport.RoundTrip(request)
	if err != nil {
		return nil, err
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		_ = response.Body.Close()
		return nil, err
	}
	if err := response.Body.Close(); err != nil {
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(body))

	r.statusCode = response.StatusCode
	r.contentType = response.Header.Get("Content-Type")
	r.responseBody = body
	return response, nil
}

func (r *prometheusResponseRecorder) golden(query string, start, end time.Time, samples []ecdf.Sample, points []PrometheusPoint) prometheusGolden {
	return prometheusGolden{
		Query:        query,
		Start:        start,
		End:          end,
		StepSeconds:  int64(prometheusGoldenStep / time.Second),
		StatusCode:   r.statusCode,
		ContentType:  r.contentType,
		ResponseBody: json.RawMessage(r.responseBody),
		WantSamples:  samples,
		WantPoints:   points,
	}
}
