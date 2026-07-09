package ecdf

import (
	"context"
	"errors"
	"time"
)

var (
	ChunkNotFoundError = errors.New("chunk not found")
)

type ChunkStore interface {
	WriteChunk(serviceId int, indicatorId int, timestamp time.Time, chunk []byte) error
	ReadChunk(serviceId int, indicatorId int, timestamp time.Time) ([]byte, error)
	ScanGoodChunks(ctx context.Context, serviceId int, indicatorId int, out chan<- []byte) error
}
