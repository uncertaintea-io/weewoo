package collection

import (
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
	LoadQuery:     "sum(delta(process_cpu_seconds_total{app=\"weewoo\"}[2m]))",
	LatencyQuery:  "sum(delta(weewoo_http_request_duration_seconds_count{app=\"weewoo\"}[2m]))",
	Interval:      time.Minute,
}

const (
	LoadLatencyIndicator = 1
	TimeOfDayIndicator   = 2
)

func NextCollectionTime(service *Service, now time.Time) time.Time {
	if service == nil || service.Interval <= 0 {
		return now
	}
	return now.Truncate(service.Interval).Add(service.Interval)
}

func (c *collector) Start() {
	c.mu.Lock()
	c.running = true
	if c.service != nil && c.next.IsZero() {
		c.next = NextCollectionTime(c.service, time.Now())
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
	c.next = NextCollectionTime(service, time.Now())
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
			next = NextCollectionTime(service, time.Now())
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
			c.next = NextCollectionTime(service, now)
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
