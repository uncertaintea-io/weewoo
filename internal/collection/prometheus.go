package collection

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	promInstantEndpoint = "/api/v1/query"
	promRangeEndpoint   = "/api/v1/query_range"
	promSuccessStatus   = "success"
	promRangeStep       = "15s"
)

type promSample []any // [timestamp, "value"]

type promInstantResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Value promSample `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

type promRangeResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Values []promSample `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// QueryPrometheusInstant returns the largest value from an instant PromQL query.
func QueryPrometheusInstant(ctx context.Context, client *http.Client, baseURL, promQL string) (float64, error) {
	var pr promInstantResponse
	if err := queryPrometheus(ctx, client, baseURL, promInstantEndpoint, url.Values{
		"query": {promQL},
	}, &pr); err != nil {
		return 0, err
	}

	if pr.Status != promSuccessStatus || len(pr.Data.Result) == 0 {
		return 0, fmt.Errorf("no data returned")
	}

	maxVal := math.Inf(-1)
	for _, r := range pr.Data.Result {
		v, err := parsePromSample(r.Value)
		if err != nil {
			continue
		}
		if v > maxVal {
			maxVal = v
		}
	}

	if math.IsInf(maxVal, -1) {
		return 0, fmt.Errorf("invalid value")
	}
	return maxVal, nil
}

// QueryPrometheusRange returns every sample value from a single-series range query.
func QueryPrometheusRange(ctx context.Context, client *http.Client, baseURL, promQL string, start, end time.Time) ([]float64, error) {
	var pr promRangeResponse
	if err := queryPrometheus(ctx, client, baseURL, promRangeEndpoint, url.Values{
		"query": {promQL},
		"start": {strconv.FormatInt(start.Unix(), 10)},
		"end":   {strconv.FormatInt(end.Unix(), 10)},
		"step":  {promRangeStep},
	}, &pr); err != nil {
		return nil, err
	}

	if pr.Status != promSuccessStatus || len(pr.Data.Result) == 0 {
		return nil, fmt.Errorf("no data returned")
	}
	if len(pr.Data.Result) != 1 {
		return nil, fmt.Errorf("unexpected number of results, got %d", len(pr.Data.Result))
	}

	samples := pr.Data.Result[0].Values
	if len(samples) == 0 {
		return nil, fmt.Errorf("no samples returned")
	}
	values := make([]float64, len(samples))
	for i, sample := range samples {
		value, err := parsePromSample(sample)
		if err != nil {
			return nil, err
		}
		values[i] = value
	}

	return values, nil
}

func queryPrometheus(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	endpoint string,
	params url.Values,
	target any,
) error {
	u, err := url.Parse(baseURL)
	if err != nil {
		return err
	}
	u.Path = strings.TrimRight(u.Path, "/") + endpoint

	q := u.Query()
	for key, values := range params {
		for _, value := range values {
			q.Add(key, value)
		}
	}
	u.RawQuery = q.Encode()
	slog.Info("Querying Prometheus", "url", u.String())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}

	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Error("Prometheus query failed", "status", resp.StatusCode)
		return fmt.Errorf("prometheus returned HTTP %d", resp.StatusCode)
	}

	err = json.NewDecoder(resp.Body).Decode(target)
	if err != nil {
		slog.Error("Failed to decode Prometheus response", "error", err)
		return err
	}
	slog.Info("Prometheus response decoded", "target", target)
	return nil
}

func parsePromSample(sample promSample) (float64, error) {
	if len(sample) != 2 {
		return 0, fmt.Errorf("unexpected number of values, got %d", len(sample))
	}

	value, ok := sample[1].(string)
	if !ok {
		return 0, fmt.Errorf("invalid value")
	}

	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid value: %w", err)
	}
	return parsed, nil
}
