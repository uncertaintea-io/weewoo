package collection

import (
	"context"
	"log"
	"net/http"
	"sync"
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
	LoadQuery:     "...",
	LatencyQuery:  "...",
	Interval:      time.Minute,
}

const (
	LoadLatencyIndicator = 1
	TimeOfDayIndicator   = 2
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

	mu       sync.Mutex
	running  bool
	next     time.Time
	wake     chan struct{}
	stop     chan struct{}
	stopOnce sync.Once
}

func Schedule(service *Service, now time.Time) (next time.Time) {
	return now.Add(service.Interval)
}

func collectSamples(client *http.Client, store ecdf.ChunkStore, service *Service, loadQuery string, latencyQuery string, now time.Time) error {
	loadValue, err := QueryPrometheusRange(context.Background(), client, service.PrometheusURL, loadQuery, now.Add(-1*service.Interval), now)
	if err != nil {
		return err
	}
	latencyValue, err := QueryPrometheusRange(context.Background(), client, service.PrometheusURL, latencyQuery, now.Add(-1*service.Interval), now)
	if err != nil {
		return err
	}
	return store.WriteChunk(service.Id, LoadLatencyIndicator, now, ecdf.CountSamples(loadValue), ecdf.CountSamples(latencyValue))
}

// NewCollector starts a collector goroutine. Call Start to begin collecting and Stop to exit it.
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
	go c.run()
	return c
}

func (c *collector) Start() {
	c.mu.Lock()
	c.running = true
	c.next = time.Now()
	c.mu.Unlock()
	c.signal()
}

func (c *collector) Stop() {
	c.stopOnce.Do(func() {
		close(c.stop)
	})
}

func (c *collector) Schedule(service *Service) {
	c.mu.Lock()
	c.service = service
	c.next = Schedule(service, time.Now())
	c.mu.Unlock()
	c.signal()
}

func (c *collector) run() {
	for {
		c.mu.Lock()
		running := c.running
		service := c.service
		next := c.next
		if running && next.IsZero() {
			next = Schedule(service, time.Now())
			c.next = next
		}
		c.mu.Unlock()

		if !running {
			select {
			case <-c.wake:
				continue
			case <-c.stop:
				return
			}
		}

		wait := time.Until(next)
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-c.wake:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				continue
			case <-c.stop:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			}
		}

		now := time.Now()
		err := collectSamples(c.client, c.store, service, service.LoadQuery, service.LatencyQuery, now)
		if err != nil {
			log.Printf("collect samples: %v", err)
		}

		c.mu.Lock()
		if c.service == service {
			c.next = Schedule(service, now)
		}
		c.mu.Unlock()
	}
}

func (c *collector) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}
