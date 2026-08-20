// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

package main

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
	"golang.org/x/sync/singleflight"
)

const (
	jointECDFRenderConcurrency = 2
	jointECDFRenderTimeout     = 15 * time.Second
	jointECDFRenderCacheBytes  = 32 << 20
)

var errJointECDFRenderBusy = errors.New("joint ECDF renderer is busy")

var (
	jointECDFRenderEvents = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "weewoo",
			Subsystem: "jecdf",
			Name:      "render_events_total",
			Help:      "Joint ECDF render cache and admission-control events.",
		},
		[]string{"event"},
	)
	jointECDFActiveRenders = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "weewoo",
			Subsystem: "jecdf",
			Name:      "active_renders",
			Help:      "Number of external jecdf render commands currently running.",
		},
	)
)

type jointECDFRenderCoordinator struct {
	render  jointECDFRenderer
	timeout time.Duration
	slots   chan struct{}
	cache   *jointECDFRenderCache
	group   singleflight.Group
}

func newJointECDFRenderCoordinator(render jointECDFRenderer, concurrency int, timeout time.Duration, cacheBytes int) *jointECDFRenderCoordinator {
	if concurrency <= 0 {
		concurrency = jointECDFRenderConcurrency
	}
	if timeout <= 0 {
		timeout = jointECDFRenderTimeout
	}
	return &jointECDFRenderCoordinator{
		render:  render,
		timeout: timeout,
		slots:   make(chan struct{}, concurrency),
		cache:   newJointECDFRenderCache(cacheBytes),
	}
}

func (c *jointECDFRenderCoordinator) Render(
	ctx context.Context,
	key string,
	definition []byte,
	width, height int,
	options ecdf.RenderOptions,
) ([]byte, error) {
	if body, ok := c.cache.Get(key); ok {
		jointECDFRenderEvents.WithLabelValues("cache_hit").Inc()
		return body, nil
	}

	result := c.group.DoChan(key, func() (any, error) {
		if body, ok := c.cache.Get(key); ok {
			jointECDFRenderEvents.WithLabelValues("cache_hit").Inc()
			return body, nil
		}

		select {
		case c.slots <- struct{}{}:
			defer func() { <-c.slots }()
		default:
			jointECDFRenderEvents.WithLabelValues("rejected").Inc()
			return nil, errJointECDFRenderBusy
		}

		// A render can be shared by callers with independent request lifetimes.
		// Keep the bounded work alive when the singleflight leader disconnects;
		// each caller still stops waiting on its own context below.
		renderCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.timeout)
		defer cancel()
		response, err := func() (*ecdf.RenderResponse, error) {
			jointECDFActiveRenders.Inc()
			defer jointECDFActiveRenders.Dec()
			return c.render(renderCtx, definition, width, height, options)
		}()
		if err != nil {
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				jointECDFRenderEvents.WithLabelValues("timed_out").Inc()
			case errors.Is(err, context.Canceled):
				jointECDFRenderEvents.WithLabelValues("canceled").Inc()
			default:
				jointECDFRenderEvents.WithLabelValues("failed").Inc()
			}
			return nil, err
		}

		body, err := json.Marshal(response)
		if err != nil {
			jointECDFRenderEvents.WithLabelValues("failed").Inc()
			return nil, err
		}
		c.cache.Add(key, body)
		jointECDFRenderEvents.WithLabelValues("rendered").Inc()
		return body, nil
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case completed := <-result:
		if completed.Shared {
			jointECDFRenderEvents.WithLabelValues("coalesced").Inc()
		}
		if completed.Err != nil {
			return nil, completed.Err
		}
		return completed.Val.([]byte), nil
	}
}

type jointECDFRenderCacheEntry struct {
	key  string
	body []byte
}

type jointECDFRenderCache struct {
	mu       sync.Mutex
	maxBytes int
	bytes    int
	entries  map[string]*list.Element
	recent   *list.List
}

func newJointECDFRenderCache(maxBytes int) *jointECDFRenderCache {
	return &jointECDFRenderCache{
		maxBytes: maxBytes,
		entries:  make(map[string]*list.Element),
		recent:   list.New(),
	}
}

func (c *jointECDFRenderCache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element := c.entries[key]
	if element == nil {
		return nil, false
	}
	c.recent.MoveToFront(element)
	return element.Value.(*jointECDFRenderCacheEntry).body, true
}

func (c *jointECDFRenderCache) Add(key string, body []byte) {
	if c.maxBytes <= 0 || len(body) > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if existing := c.entries[key]; existing != nil {
		entry := existing.Value.(*jointECDFRenderCacheEntry)
		c.bytes -= len(entry.body)
		entry.body = body
		c.bytes += len(body)
		c.recent.MoveToFront(existing)
	} else {
		entry := &jointECDFRenderCacheEntry{key: key, body: body}
		c.entries[key] = c.recent.PushFront(entry)
		c.bytes += len(body)
	}
	for c.bytes > c.maxBytes {
		oldest := c.recent.Back()
		entry := oldest.Value.(*jointECDFRenderCacheEntry)
		delete(c.entries, entry.key)
		c.bytes -= len(entry.body)
		c.recent.Remove(oldest)
	}
}
