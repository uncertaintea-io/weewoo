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

func (c *fakeChunkStore) WriteChunk(serviceId int, indicatorId int, timestamp time.Time, x, y []Sample) error {
	if _, ok := c.chunks[serviceId]; !ok {
		c.chunks[serviceId] = make(map[int]map[time.Time]TimeChunk)
	}
	if _, ok := c.chunks[serviceId][indicatorId]; !ok {
		c.chunks[serviceId][indicatorId] = make(map[time.Time]TimeChunk)
	}
	c.chunks[serviceId][indicatorId][timestamp] = TimeChunk{Timestamp: timestamp, X: x, Y: y}
	return nil
}

var (
	ChunkNotFoundError = errors.New("chunk not found")
)

func (c *fakeChunkStore) ReadChunk(serviceId int, indicatorId int, timestamp time.Time) (TimeChunk, error) {
	indicators, ok := c.chunks[serviceId]
	if !ok {
		return TimeChunk{}, ChunkNotFoundError
	}
	chunks, ok := indicators[indicatorId]
	if !ok {
		return TimeChunk{}, ChunkNotFoundError
	}
	chunk, ok := chunks[timestamp]
	if !ok {
		return TimeChunk{}, ChunkNotFoundError
	}
	return chunk, nil
}
