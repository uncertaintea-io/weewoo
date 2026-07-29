package ecdf

import (
	"context"
	"slices"
	"time"
)

type fakeEntry struct {
	timestamp  time.Time
	chunk      []byte
	good       *bool
	baseline   bool
	review     *bool
	pValue     float64
	generation int64
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

func (c *fakeChunkStore) WriteChunk(serviceId, indicatorId int, generation int64, timestamp time.Time, chunk []byte) error {
	if _, ok := c.chunks[serviceId]; !ok {
		c.chunks[serviceId] = make(map[int][]*fakeEntry)
	}
	entry := &fakeEntry{timestamp: timestamp, chunk: chunk, baseline: true, generation: generation}
	entries := c.chunks[serviceId][indicatorId]
	i, found := slices.BinarySearchFunc(entries, entry, (*fakeEntry).Compare)
	if found {
		if entries[i].generation > generation {
			return nil
		}
		if entries[i].generation < generation {
			entries[i] = entry
			c.chunks[serviceId][indicatorId] = entries
			return nil
		}
		entry.good = entries[i].good
		entry.baseline = entries[i].baseline
		entry.review = entries[i].review
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

func (c *fakeChunkStore) WriteVerdict(_ context.Context, serviceID, indicatorID int, generation int64, timestamp time.Time, good bool, pValue float64) error {
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
	if entries[i].generation != generation {
		return nil
	}
	entries[i].good = new(bool)
	*entries[i].good = good
	entries[i].baseline = false
	entries[i].pValue = pValue
	return nil
}

func (c *fakeChunkStore) CountEligibleChunks(_ context.Context, serviceID, indicatorID int, generation int64) (int, error) {
	entries := c.chunks[serviceID][indicatorID]
	count := 0
	for _, entry := range entries {
		if entry.generation != generation {
			continue
		}
		if entry.baseline || (entry.good != nil && *entry.good) || (entry.good != nil && !*entry.good && entry.review != nil && *entry.review) {
			count++
		}
	}
	return count, nil
}

func (c *fakeChunkStore) ScanGoodChunks(ctx context.Context, serviceId, indicatorId int, generation int64, out chan<- []byte) error {
	indicators, ok := c.chunks[serviceId]
	if !ok {
		return nil
	}
	entries, ok := indicators[indicatorId]
	if !ok {
		return nil
	}
	for _, entry := range entries {
		if entry.generation != generation {
			continue
		}
		eligible := entry.baseline || (entry.good != nil && *entry.good) ||
			(entry.good != nil && !*entry.good && entry.review != nil && *entry.review)
		if !eligible {
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
