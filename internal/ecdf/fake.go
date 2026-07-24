package ecdf

import (
	"context"
	"slices"
	"time"
)

type fakeEntry struct {
	timestamp time.Time
	chunk     []byte
	good      *bool
	pValue    float64
}

func (c *fakeEntry) Compare(other *fakeEntry) int {
	return c.timestamp.Compare(other.timestamp)
}

type fakeChunkStore struct {
	chunks map[int]map[int][]*fakeEntry // serviceId -> indicatorId -> []fakeEntry
}

func NewFakeChunkStore() ChunkStore {
	return &fakeChunkStore{chunks: make(map[int]map[int][]*fakeEntry)}
}

func (c *fakeChunkStore) WriteChunk(serviceId int, indicatorId int, timestamp time.Time, chunk []byte) error {
	if _, ok := c.chunks[serviceId]; !ok {
		c.chunks[serviceId] = make(map[int][]*fakeEntry)
	}
	entry := &fakeEntry{timestamp: timestamp, chunk: chunk}
	entries := c.chunks[serviceId][indicatorId]
	i, found := slices.BinarySearchFunc(entries, entry, (*fakeEntry).Compare)
	if found {
		entry.good = entries[i].good
		entry.pValue = entries[i].pValue
		entries[i] = entry
	} else {
		entries = slices.Insert(entries, i, entry)
	}
	c.chunks[serviceId][indicatorId] = entries
	return nil
}

func (c *fakeChunkStore) ReadChunk(serviceId int, indicatorId int, timestamp time.Time) ([]byte, error) {
	indicators, ok := c.chunks[serviceId]
	if !ok {
		return nil, ChunkNotFoundError
	}
	entries, ok := indicators[indicatorId]
	if !ok {
		return nil, ChunkNotFoundError
	}
	i, found := slices.BinarySearchFunc(entries, timestamp, func(entry *fakeEntry, timestamp time.Time) int {
		return entry.timestamp.Compare(timestamp)
	})
	if !found {
		return nil, ChunkNotFoundError
	}
	return entries[i].chunk, nil
}

func (c *fakeChunkStore) WriteVerdict(_ context.Context, serviceID, indicatorID int, timestamp time.Time, good bool, pValue float64) error {
	indicators, ok := c.chunks[serviceID]
	if !ok {
		return ChunkNotFoundError
	}
	entries, ok := indicators[indicatorID]
	if !ok {
		return ChunkNotFoundError
	}
	i, found := slices.BinarySearchFunc(entries, timestamp, func(entry *fakeEntry, timestamp time.Time) int {
		return entry.timestamp.Compare(timestamp)
	})
	if !found {
		return ChunkNotFoundError
	}
	entries[i].good = new(bool)
	*entries[i].good = good
	entries[i].pValue = pValue
	return nil
}

func (c *fakeChunkStore) ScanGoodChunks(ctx context.Context, serviceId int, indicatorId int, out chan<- []byte) error {
	indicators, ok := c.chunks[serviceId]
	if !ok {
		return nil
	}
	entries, ok := indicators[indicatorId]
	if !ok {
		return nil
	}
	for _, entry := range entries {
		if entry.good != nil && !*entry.good {
			continue
		}
		select {
		case out <- entry.chunk:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
