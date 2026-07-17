package collection

import (
	"bytes"
	"context"
	"io"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

type unreadJointStore struct{}

func (unreadJointStore) Publish(context.Context, int, int, time.Time, func(io.Writer) error) (int64, bool, error) {
	panic("unexpected Publish call")
}

func TestAnalyseSampleRejectsUnsafeExpandedSampleCount(t *testing.T) {
	const serviceID, indicatorID = 1, 2
	timestamp := time.Unix(1_700_000_000, 0)
	chunk, err := ecdf.Encode(
		timestamp,
		[]ecdf.Sample{{Value: 12, Count: 1}},
		[]ecdf.Sample{{Value: 30, Count: math.MaxUint64}},
	)
	require.NoError(t, err)

	chunks := ecdf.NewFakeChunkStore()
	require.NoError(t, chunks.WriteChunk(serviceID, indicatorID, timestamp, chunk))

	_, err = AnalyseSample(chunks, config.NewFakeConfig(), unreadJointStore{}, serviceID, indicatorID, timestamp)
	require.EqualError(t, err, "invalid latency samples: observation count 18446744073709551615 exceeds limit 1000000")
}

func (unreadJointStore) ReadCurrent(context.Context, int, int) ([]byte, error) {
	panic("unexpected ReadCurrent call")
}

func TestAnalyseSampleRejectsZeroTotalSampleCount(t *testing.T) {
	const serviceID, indicatorID = 1, 2
	timestamp := time.Unix(1_700_000_000, 0)

	for _, test := range []struct {
		name      string
		loads     []ecdf.Sample
		latencies []ecdf.Sample
		wantError string
	}{
		{"load", []ecdf.Sample{{Value: 12, Count: 0}}, []ecdf.Sample{{Value: 30, Count: 1}}, "chunk has no load observations"},
		{"latency", []ecdf.Sample{{Value: 12, Count: 1}}, []ecdf.Sample{{Value: 30, Count: 0}}, "chunk has no latency observations"},
	} {
		t.Run(test.name, func(t *testing.T) {
			chunk, err := ecdf.Encode(timestamp, test.loads, test.latencies)
			require.NoError(t, err)

			chunks := ecdf.NewFakeChunkStore()
			require.NoError(t, chunks.WriteChunk(serviceID, indicatorID, timestamp, bytes.Clone(chunk)))

			_, err = AnalyseSample(chunks, config.NewFakeConfig(), unreadJointStore{}, serviceID, indicatorID, timestamp)
			require.EqualError(t, err, test.wantError)
		})
	}
}
