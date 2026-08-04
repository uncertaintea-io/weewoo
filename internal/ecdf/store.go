package ecdf

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ChunkNotFoundError = errors.New("chunk not found")
)

type ChunkStore interface {
	WriteChunk(serviceID, indicatorID int, generation int64, timestamp time.Time, chunk []byte) error
	ReadChunk(serviceId int, indicatorId int, timestamp time.Time) ([]byte, error)
	WriteVerdict(ctx context.Context, serviceID, indicatorID int, generation int64, timestamp time.Time, good bool, pValue float64) error
	CountEligibleChunks(ctx context.Context, serviceID, indicatorID int, generation int64) (int, error)
	ScanGoodChunks(ctx context.Context, serviceID, indicatorID int, generation int64, out chan<- []byte) error
}

// JointStore atomically publishes and reads generated joint ECDFs. Publish
// returns published=false when another process is already publishing the same
// service and indicator.
type JointStore interface {
	Publish(ctx context.Context, serviceID, indicatorID int, intervalEnd time.Time, build func(io.Writer) error) (bytesWritten int64, published bool, err error)

	// ReadCurrent returns the latest published joint ECDF and its SHA256 checksum.
	ReadCurrent(ctx context.Context, serviceID, indicatorID int) ([]byte, string, error)
}
