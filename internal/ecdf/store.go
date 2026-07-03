package ecdf

import (
	"errors"
	"time"
)

var (
	ChunkNotFoundError = errors.New("chunk not found")
)

type ChunkStore interface {
	WriteChunk(serviceId int, indicatorId int, timestamp time.Time, chunk []byte) error
	ReadChunk(serviceId int, indicatorId int, timestamp time.Time) ([]byte, error)
	ScanGoodChunks(serviceId int, indicatorId int, start, end time.Time, out chan<- []byte) error
}
