package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"time"
)

/* ANSI colors */
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorGray   = "\033[90m"
)

type promResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Value []any `json:"value"` // [ timestamp, "value" ]
		} `json:"result"`
	} `json:"data"`
}

func queryPrometheus(ctx context.Context, client *http.Client, baseURL, promQL string) (float64, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return 0, err
	}
	u.Path = "/api/v1/query"

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

	var pr promResponse
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
		var v float64
		fmt.Sscanf(valStr, "%f", &v)
		if v > maxVal {
			maxVal = v
		}
	}

	if math.IsInf(maxVal, -1) {
		return 0, fmt.Errorf("invalid value")
	}
	return maxVal, nil
}

func main() {
	promURL := flag.String("url", "http://localhost:9090", "Prometheus URL")
	threshold := flag.Float64("threshold", 75, "Threshold percent")
	overFor := flag.Duration("duration", 10*time.Second, "Time over threshold to trigger error")
	interval := flag.Duration("interval", 1*time.Second, "Polling interval")
	timeout := flag.Duration("timeout", 5*time.Second, "HTTP timeout")
	flag.Parse()

	const promQL = `100 * (1 - (windows_memory_available_bytes / windows_memory_physical_total_bytes))`

	client := &http.Client{Timeout: *timeout}
	var aboveSince *time.Time

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	fmt.Println(colorGray + "Starting memory monitor" + colorReset)
	fmt.Printf("Threshold: %.2f%% for %s\n\n", *threshold, overFor)

	for {
		<-ticker.C

		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		val, err := queryPrometheus(ctx, client, *promURL, promQL)
		cancel()

		now := time.Now().Format("15:04:05")

		if err != nil {
			fmt.Printf("%s[%s] ERROR querying Prometheus: %v%s\n",
				colorRed, now, err, colorReset)
			continue
		}

		switch {
		case val < *threshold:
			aboveSince = nil
			fmt.Printf("%s[%s] Memory: %.2f%%%s\n",
				colorGreen, now, val, colorReset)

		case aboveSince == nil:
			t := time.Now()
			aboveSince = &t
			fmt.Printf("%s[%s] Memory: %.2f%% (above threshold)%s\n",
				colorYellow, now, val, colorReset)

		case time.Since(*aboveSince) < *overFor:
			fmt.Printf("%s[%s] Memory: %.2f%% (%.0fs / %.0fs)%s\n",
				colorYellow,
				now,
				val,
				time.Since(*aboveSince).Seconds(),
				overFor.Seconds(),
				colorReset)

		default:
			fmt.Printf("%s[%s] Memory: %.2f%% — ERROR ERROR ERROR%s\n",
				colorRed, now, val, colorReset)
			os.Exit(2)
		}
	}
}
