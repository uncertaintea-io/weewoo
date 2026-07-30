package collection

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueryPrometheusRangeIncludesPrometheusErrorDetails(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
			"status":"error",
			"errorType":"bad_data",
			"error":"invalid parameter \"query\": parse error at char 12"
		}`)),
		}, nil
	})}

	_, err := QueryPrometheusRange(
		context.Background(),
		client,
		"http://prometheus.example.com",
		"broken query",
		time.Unix(0, 0),
		time.Unix(60, 0),
	)

	require.Error(t, err)
	assert.Equal(
		t,
		`prometheus returned HTTP 400 (bad_data): invalid parameter "query": parse error at char 12`,
		err.Error(),
	)
}
