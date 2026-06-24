package ecdf

import (
	"errors"
	"time"
)

type fakeChunkStore struct {
	chunks map[int]map[int]map[time.Time]TimeChunk
}

func NewFakeChunkStore() ChunkStore {
	return &fakeChunkStore{chunks: make(map[int]map[int]map[time.Time]TimeChunk)}
}

func (c *fakeChunkStore) WriteChunk(service_id int, indicator_id int, timestamp time.Time, x, y []Sample) error {
	if _, ok := c.chunks[service_id]; !ok {
		c.chunks[service_id] = make(map[int]map[time.Time]TimeChunk)
	}
	if _, ok := c.chunks[service_id][indicator_id]; !ok {
		c.chunks[service_id][indicator_id] = make(map[time.Time]TimeChunk)
	}
	c.chunks[service_id][indicator_id][timestamp] = TimeChunk{Timestamp: timestamp, X: x, Y: y}
	return nil
}

func (c *fakeChunkStore) ReadChunk(service_id int, indicator_id int, timestamp time.Time) (TimeChunk, error) {
	indicators, ok := c.chunks[service_id]
	if !ok {
		return TimeChunk{}, errors.New("chunk not found")
	}
	chunks, ok := indicators[indicator_id]
	if !ok {
		return TimeChunk{}, errors.New("chunk not found")
	}
	chunk, ok := chunks[timestamp]
	if !ok {
		return TimeChunk{}, errors.New("chunk not found")
	}
	return chunk, nil
}
