package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/uncertaintea-io/weewoo/internal/collection"
	"github.com/uncertaintea-io/weewoo/internal/config"
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

func monitorCpu(config config.Config) {
	promURL := "http://pc0:9090"
	//threshold := flag.Float64("threshold", 0.25, "Threshold percent")
	//overFor := flag.Duration("duration", 5*time.Minute, "Time over threshold to trigger error")
	//interval := flag.Duration("interval", 15*time.Second, "Polling interval")
	timeout := 5 * time.Second
	//once := flag.Bool("once", false, "Run the query once and print only the value")
	targets := false

	if targets {
		printTargets(promURL)
		return
	}

	client := &http.Client{Timeout: timeout}
	end := time.Now()
	start := end.Add(-5 * time.Minute)
	values, err := collection.QueryPrometheusRange(context.Background(), client, promURL, cpuQuery, start, end)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(MakeECDF(values))
}

func main() {
	configfile := flag.String("config", "config.yaml", "Config file")
	flag.Parse()
	systemSettings, err := config.ReadSystemSettings(*configfile)
	if err != nil {
		log.Fatalf("Failed to read system settings: %v", err)
	}
	config, err := config.NewDatabaseConfig(systemSettings.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to create database config: %v", err)
	}
	monitorCpu(config)

}
