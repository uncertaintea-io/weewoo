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

func (c *fakeChunkStore) WriteChunk(service_Id int, indicator_Id int, timestamp time.Time, x, y []Sample) error {
	if _, ok := c.chunks[service_Id]; !ok {
		c.chunks[service_Id] = make(map[int]map[time.Time]TimeChunk)
	}
	if _, ok := c.chunks[service_Id][indicator_Id]; !ok {
		c.chunks[service_Id][indicator_Id] = make(map[time.Time]TimeChunk)
	}
	c.chunks[service_Id][indicator_Id][timestamp] = TimeChunk{Timestamp: timestamp, X: x, Y: y}
	return nil
}

var (
	ChunkNotFoundError = errors.New("chunk not found")
)

func (c *fakeChunkStore) ReadChunk(service_Id int, indicator_Id int, timestamp time.Time) (TimeChunk, error) {
	indicators, ok := c.chunks[service_Id]
	if !ok {
		return TimeChunk{}, ChunkNotFoundError
	}
	chunks, ok := indicators[indicator_Id]
	if !ok {
		return TimeChunk{}, ChunkNotFoundError
	}
	chunk, ok := chunks[timestamp]
	if !ok {
		return TimeChunk{}, ChunkNotFoundError
	}
	return chunk, nil
}
