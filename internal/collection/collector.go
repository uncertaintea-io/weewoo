package collection

import (
	"context"
	"net/http"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

type Service struct {
	Id            int
	Name          string
	PrometheusURL string
	LoadQuery     string
	LatencyQuery  string
	Interval      time.Duration
}

var WeeWooService = &Service{
	Id:            1,
	Name:          "WeeWoo",
	PrometheusURL: "http://pc0:9090",
	LoadQuery:     "sum(delta(weewoo_http_request_duration_seconds_count{app=\"weewoo\"}[2m]))",
	LatencyQuery:  "histogram_quantile(0.99,weewoo_http_request_duration_seconds{app=\"weewoo\"}) or on() vector(0)",
	Interval:      time.Minute,
}

const (
	LoadLatencyIndicator = 1
	TimeOfDayIndicator   = 2
)

type Collector interface {
	Stop()
	Schedule(service *Service)
}

type collector struct {
	client    *http.Client
	store     ecdf.ChunkStore
	service   *Service
	scheduler *IntervalScheduler
}

// this creates a collector that can be used to collect samples from the prometheus server
func NewCollector(client *http.Client, store ecdf.ChunkStore, service *Service) Collector {
	if client == nil {
		client = http.DefaultClient
	}
	c := &collector{
		client:    client,
		store:     store,
		service:   service,
		scheduler: NewIntervalScheduler(),
	}
	return c
}

func (c *collector) Stop() {
	c.scheduler.Stop()
}

func (c *collector) Schedule(service *Service) {
	c.scheduler.AddCallback(service.Id, service.Interval, func(ctx context.Context, start time.Time, end time.Time) IntervalResult {
		if err := c.collectSamples(ctx, service, start, end); err != nil {
			return IntervalRetry(err)
		}
		return IntervalSuccess()
	})
}

// this collects the samples from the prometheus server and writes them to the chunk store
func (c *collector) collectSamples(ctx context.Context, service *Service, start, end time.Time) error {
	loadValue, err := QueryPrometheusRange(ctx, c.client, service.PrometheusURL, service.LoadQuery, start, end)
	if err != nil {
		return err
	}
	latencyValue, err := QueryPrometheusRange(ctx, c.client, service.PrometheusURL, service.LatencyQuery, start, end)
	if err != nil {
		return err
	}
	return c.store.WriteChunk(service.Id, LoadLatencyIndicator, end, ecdf.CountSamples(loadValue), ecdf.CountSamples(latencyValue))
}
