package collection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrNoPrometheusData = errors.New("no data returned")

const (
	promInstantEndpoint = "/api/v1/query"
	promRangeEndpoint   = "/api/v1/query_range"
	promSuccessStatus   = "success"
	promRangeStep       = "15s"
	promErrorBodyLimit  = 8 << 10
)

type promSample []any // [timestamp, "value"]

type prometheusPoint struct {
	Timestamp time.Time
	Value     float64
}

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

type promErrorResponse struct {
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
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
		return 0, ErrNoPrometheusData
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
	points, err := queryPrometheusRangePoints(ctx, client, baseURL, promQL, start, end)
	if err != nil {
		return nil, err
	}
	values := make([]float64, len(points))
	for i, point := range points {
		values[i] = point.Value
	}
	return values, nil
}

func queryPrometheusRangePoints(ctx context.Context, client *http.Client, baseURL, promQL string, start, end time.Time) ([]prometheusPoint, error) {
	return queryPrometheusRangePointsAtStep(ctx, client, baseURL, promQL, start, end, 15*time.Second)
}

func queryPrometheusRangePointsAtStep(ctx context.Context, client *http.Client, baseURL, promQL string, start, end time.Time, step time.Duration) ([]prometheusPoint, error) {
	if step <= 0 || step%time.Second != 0 {
		return nil, fmt.Errorf("Prometheus range step must be a positive whole number of seconds")
	}
	var pr promRangeResponse
	if err := queryPrometheus(ctx, client, baseURL, promRangeEndpoint, url.Values{
		"query": {promQL},
		"start": {strconv.FormatInt(start.Unix(), 10)},
		"end":   {strconv.FormatInt(end.Unix(), 10)},
		"step":  {strconv.FormatInt(int64(step/time.Second), 10) + "s"},
	}, &pr); err != nil {
		return nil, err
	}

	if pr.Status != promSuccessStatus || len(pr.Data.Result) == 0 {
		return nil, ErrNoPrometheusData
	}
	if len(pr.Data.Result) != 1 {
		return nil, fmt.Errorf("unexpected number of results, got %d", len(pr.Data.Result))
	}

	samples := pr.Data.Result[0].Values
	if len(samples) == 0 {
		return nil, fmt.Errorf("%w: no samples returned", ErrNoPrometheusData)
	}
	points := make([]prometheusPoint, len(samples))
	for i, sample := range samples {
		value, err := parsePromSample(sample)
		if err != nil {
			return nil, err
		}
		timestamp, err := parsePromTimestamp(sample)
		if err != nil {
			return nil, err
		}
		points[i] = prometheusPoint{Timestamp: timestamp, Value: value}
	}

	return points, nil
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
		queryErr := prometheusResponseError(resp)
		slog.Error("Prometheus query failed", "status", resp.StatusCode, "error", queryErr)
		return queryErr
	}

	err = json.NewDecoder(resp.Body).Decode(target)
	if err != nil {
		slog.Error("Failed to decode Prometheus response", "error", err)
		return err
	}
	slog.Debug("Prometheus response decoded")
	return nil
}

func prometheusResponseError(resp *http.Response) error {
	base := fmt.Sprintf("prometheus returned HTTP %d", resp.StatusCode)
	var detail promErrorResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, promErrorBodyLimit)).Decode(&detail); err != nil || detail.Error == "" {
		return fmt.Errorf("%s", base)
	}
	if detail.ErrorType == "" {
		return fmt.Errorf("%s: %s", base, detail.Error)
	}
	return fmt.Errorf("%s (%s): %s", base, detail.ErrorType, detail.Error)
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

func parsePromTimestamp(sample promSample) (time.Time, error) {
	if len(sample) != 2 {
		return time.Time{}, fmt.Errorf("unexpected number of values, got %d", len(sample))
	}
	seconds, ok := sample[0].(float64)
	if !ok {
		return time.Time{}, fmt.Errorf("invalid timestamp")
	}
	wholeSeconds, fractionalSeconds := math.Modf(seconds)
	return time.Unix(int64(wholeSeconds), int64(fractionalSeconds*float64(time.Second))).UTC(), nil
}
