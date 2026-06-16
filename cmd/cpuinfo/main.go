package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
)

/* ANSI colors */
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorGray   = "\033[90m"
	cpuQuery    = "sum(delta(process_cpu_seconds_total{app=\"weewoo\"}[2m]))"
)

type promInstantResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Value []any `json:"value"` // [ timestamp, "value" ]
		} `json:"result"`
	} `json:"data"`
}

type promRangeResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Values [][]any `json:"values"` // [ timestamp, "value" ]
		} `json:"result"`
	} `json:"data"`
}

func queryPrometheusInstant(ctx context.Context, client *http.Client, baseURL, promQL string) (float64, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return 0, err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/query"

	q := u.Query()
	q.Set("query", promQL)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("prometheus returned HTTP %d", resp.StatusCode)
	}

	var pr promInstantResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return 0, err
	}
	if pr.Status != "success" || len(pr.Data.Result) == 0 {
		return 0, fmt.Errorf("no data returned")
	}

	maxVal := math.Inf(-1)
	for _, r := range pr.Data.Result {
		if len(r.Value) != 2 {
			continue
		}
		valStr, ok := r.Value[1].(string)
		if !ok {
			continue
		}
		v, err := strconv.ParseFloat(valStr, 64)
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

func queryPrometheusRange(ctx context.Context, client *http.Client, baseURL, promQL string, start, end time.Time) ([]float64, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/query_range"

	q := u.Query()
	q.Set("query", promQL)
	q.Set("start", start.Format(time.RFC3339))
	q.Set("end", end.Format(time.RFC3339))
	q.Set("step", "15s")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("prometheus returned HTTP %d", resp.StatusCode)
	}

	var pr promRangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, err
	}
	if pr.Status != "success" || len(pr.Data.Result) == 0 {
		return nil, fmt.Errorf("no data returned")
	}

	if len(pr.Data.Result) != 1 {
		return nil, fmt.Errorf("unexpected number of results, got %d", len(pr.Data.Result))
	}
	result := pr.Data.Result[0].Values
	values := make([]float64, len(result))
	for i, v := range result {
		if len(v) != 2 {
			return nil, fmt.Errorf("unexpected number of values, got %d", len(v))
		}
		valStr, ok := v[1].(string)
		if !ok {
			return nil, fmt.Errorf("invalid value")
		}
		v, err := strconv.ParseFloat(valStr, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid value: %v", err)
		}
		values[i] = v
	}
	return values, nil
}

func printTargets(promURL string) {
	config := api.Config{
		Address: promURL,
	}
	client, err := api.NewClient(config)
	if err != nil {
		log.Fatal(err)
	}
	api := v1.NewAPI(client)
	targets, err := api.Targets(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	for _, at := range targets.Active {
		fmt.Println(at.ScrapeURL)
	}
}

func monitorCpu() {
	promURL := flag.String("url", "http://pc0:9090", "Prometheus URL")
	//threshold := flag.Float64("threshold", 0.25, "Threshold percent")
	//overFor := flag.Duration("duration", 5*time.Minute, "Time over threshold to trigger error")
	//interval := flag.Duration("interval", 15*time.Second, "Polling interval")
	timeout := flag.Duration("timeout", 5*time.Second, "HTTP timeout")
	//once := flag.Bool("once", false, "Run the query once and print only the value")
	targets := flag.Bool("targets", false, "Print Prometheus scrape targets and exit")
	flag.Parse()

	if *targets {
		printTargets(*promURL)
		return
	}

	client := &http.Client{Timeout: *timeout}
	end := time.Now()
	start := end.Add(-5 * time.Minute)
	values, err := queryPrometheusRange(context.Background(), client, *promURL, cpuQuery, start, end)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(MakeECDF(values))
}

func main() {
	monitorCpu()

}
