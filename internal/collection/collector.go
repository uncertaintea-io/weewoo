package collection

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

type Collector interface {
	Start()
	Stop()
	Schedule(service *Service)
}

type collector struct {
	client  *http.Client
	store   ecdf.ChunkStore
	service *Service

	mu        sync.Mutex
	running   bool
	next      time.Time
	wake      chan struct{}
	stop      chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
}

// this collects the samples from the prometheus server and writes them to the chunk store
func collectSamples(client *http.Client, store ecdf.ChunkStore, service *Service, next time.Time) error {
	loadValue, err := QueryPrometheusRange(context.Background(), client, service.PrometheusURL, service.LoadQuery, next.Add(-1*service.Interval), next)
	if err != nil {
		return err
	}
	latencyValue, err := QueryPrometheusRange(context.Background(), client, service.PrometheusURL, service.LatencyQuery, next.Add(-1*service.Interval), next)
	if err != nil {
		return err
	}
	return store.WriteChunk(service.Id, LoadLatencyIndicator, next, ecdf.CountSamples(loadValue), ecdf.CountSamples(latencyValue))
}

// this creates a collector that can be used to collect samples from the prometheus server
func NewCollector(client *http.Client, store ecdf.ChunkStore, service *Service) Collector {
	if client == nil {
		client = http.DefaultClient
	}
	c := &collector{
		client:  client,
		store:   store,
		service: service,
		wake:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
	}
	return c
}
