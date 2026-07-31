package collection

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

func TestTimeOfDayBucketUsesServiceIntervalFromUTCMidnight(t *testing.T) {
	timestamp := time.Date(2026, 7, 31, 1, 2, 59, 0, time.FixedZone("west", -4*60*60))
	// 05:02:59 UTC belongs to the 05:02:30 bucket at a 30-second interval.
	assert.Equal(t, float64((5*3600+2*60+59)/30), timeOfDayBucket(timestamp, 30*time.Second))
}

func TestWriteTimeOfDayChunksPreservesSingletonPairAndObservationTimestamp(t *testing.T) {
	store := ecdf.NewFakeChunkStore()
	c := &collector{chunkStore: store}
	service := &config.Service{Id: 7, Generation: 2, Interval: 30 * time.Second}
	timestamp := time.Date(2026, 7, 31, 12, 0, 15, 900, time.UTC)

	observations, err := c.writeTimeOfDayChunks(service, []prometheusPoint{{Timestamp: timestamp, Value: 42}})
	require.NoError(t, err)
	require.Len(t, observations, 1)
	storedAt := timestamp.Truncate(time.Second)
	body, err := store.ReadChunk(7, TimeOfDayIndicator, storedAt)
	require.NoError(t, err)
	encodedAt, x, y, err := ecdf.Decode(body)
	require.NoError(t, err)
	assert.True(t, storedAt.Equal(encodedAt))
	assert.Equal(t, []ecdf.Sample{{Value: float64(12 * 3600 / 30), Count: 1}}, x)
	assert.Equal(t, []ecdf.Sample{{Value: 42, Count: 1}}, y)
}

func TestTimeOfDayCoverageRequiresFiveDistinctDatesForNinetyFivePercentOfBuckets(t *testing.T) {
	store := ecdf.NewFakeChunkStore()
	const serviceID = 3
	interval := 12 * time.Hour // two buckets keeps the fixture small; both must qualify for >=95%.
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	for day := range 5 {
		for bucket := range 2 {
			timestamp := start.AddDate(0, 0, day).Add(time.Duration(bucket) * interval)
			chunk, err := ecdf.Encode(timestamp, []ecdf.Sample{{Value: float64(bucket), Count: 1}}, []ecdf.Sample{{Value: 10, Count: 1}})
			require.NoError(t, err)
			require.NoError(t, store.WriteChunk(serviceID, TimeOfDayIndicator, 1, timestamp, chunk))
		}
	}
	coverage, ready, err := timeOfDayCoverage(context.Background(), store, serviceID, 1, interval)
	require.NoError(t, err)
	assert.Equal(t, 1.0, coverage)
	assert.True(t, ready)
}
