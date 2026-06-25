package collection

import (
	"fmt"
	"log"
	"time"
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

func NextCollectionTime(service *Service, now time.Time) (time.Time, error) {
	if service == nil {
		return now, nil
	}
	if service.Interval <= 0 {
		return now, fmt.Errorf("collection interval is not set")
	}
	return now.Truncate(service.Interval).Add(service.Interval), nil
}

func (c *collector) Start() {
	c.mu.Lock()
	c.running = true
	if c.service != nil && c.next.IsZero() {
		next, err := NextCollectionTime(c.service, time.Now())
		if err != nil {
			log.Printf("next collection time: %v", err)
		}
		c.next = next
	}
	c.mu.Unlock()

	c.startOnce.Do(func() {
		go c.run()
	})
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
	next, err := NextCollectionTime(service, time.Now())
	if err != nil {
		log.Printf("next collection time: %v", err)
	}
	c.next = next
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
			next, err := NextCollectionTime(service, time.Now())
			if err != nil {
				log.Printf("next collection time: %v", err)
			}
			c.next = next
		}
		c.mu.Unlock()

		if !running || service == nil {
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
		err := collectSamples(c.client, c.store, service, next)
		if err != nil {
			log.Printf("collect samples: %v", err)
		}

		c.mu.Lock()
		if c.service == service {
			next, err := NextCollectionTime(service, now)
			if err != nil {
				log.Printf("next collection time: %v", err)
			}
			c.next = next
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
