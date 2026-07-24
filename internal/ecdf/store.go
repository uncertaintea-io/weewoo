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
	WriteChunk(serviceId int, indicatorId int, timestamp time.Time, chunk []byte) error
	ReadChunk(serviceId int, indicatorId int, timestamp time.Time) ([]byte, error)
	WriteVerdict(ctx context.Context, serviceID, indicatorID int, timestamp time.Time, good bool, pValue float64) error
	ScanGoodChunks(ctx context.Context, serviceId int, indicatorId int, out chan<- []byte) error
}

// JointStore atomically publishes and reads generated joint ECDFs. Publish
// returns published=false when another process is already publishing the same
// service and indicator.
type JointStore interface {
	Publish(ctx context.Context, serviceID, indicatorID int, intervalEnd time.Time, build func(io.Writer) error) (bytesWritten int64, published bool, err error)
	ReadCurrent(ctx context.Context, serviceID, indicatorID int) ([]byte, error)
}
