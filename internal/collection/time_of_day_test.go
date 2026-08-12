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

type fixedEligibleChunkStore struct {
	ecdf.ChunkStore
	eligible int
}

func (s *fixedEligibleChunkStore) CountEligibleChunks(context.Context, int, int, int64) (int, error) {
	return s.eligible, nil
}

func TestTimeOfDayReadinessUsesEligibleChunksOverFiveDayTrainingRange(t *testing.T) {
	// Five days at a 15-second interval contain 28,800 expected time chunks.
	// The 95% readiness threshold is therefore 27,360 eligible chunks.
	store := &fixedEligibleChunkStore{ChunkStore: ecdf.NewFakeChunkStore(), eligible: 27_359}
	service := &config.Service{Id: 3, Generation: 1, Interval: 15 * time.Second}

	readiness, err := ReadModelReadiness(context.Background(), config.NewFakeConfig(), store, service, TimeOfDayIndicator)
	require.NoError(t, err)
	assert.InDelta(t, float64(27_359)/28_800, readiness.Coverage, 0.0000001)
	assert.False(t, readiness.Ready)

	store.eligible++
	readiness, err = ReadModelReadiness(context.Background(), config.NewFakeConfig(), store, service, TimeOfDayIndicator)
	require.NoError(t, err)
	assert.Equal(t, 0.95, readiness.Coverage)
	assert.Equal(t, readiness.Coverage, readiness.Progress)
	assert.Equal(t, 5, readiness.Required)
	assert.Equal(t, 27_360, readiness.Eligible)
	assert.True(t, readiness.Ready)
}

func TestTimeOfDayCoverageCountsOnlyEligibleChunks(t *testing.T) {
	store := ecdf.NewFakeChunkStore()
	service := &config.Service{Id: 7, Interval: 12 * time.Hour, Generation: 1}
	start := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	for chunkNumber := range 5 {
		timestamp := start.Add(time.Duration(chunkNumber) * service.Interval)
		chunk, err := ecdf.Encode(timestamp, []ecdf.Sample{{Value: float64(chunkNumber), Count: 1}}, []ecdf.Sample{{Value: 42, Count: 1}})
		require.NoError(t, err)
		require.NoError(t, store.WriteChunk(service.Id, TimeOfDayIndicator, service.Generation, timestamp, chunk))
	}
	require.NoError(t, store.WriteVerdict(context.Background(), service.Id, TimeOfDayIndicator, service.Generation, start, false, 0.01))

	readiness, err := ReadModelReadiness(context.Background(), config.NewFakeConfig(), store, service, TimeOfDayIndicator)

	require.NoError(t, err)
	// Five days contain ten expected 12-hour chunks. One of the five received
	// chunks is Bad, leaving four eligible chunks.
	assert.Equal(t, 0.4, readiness.Coverage)
	assert.InDelta(t, 0.4, readiness.Progress, 0.0001)
	assert.Equal(t, 4, readiness.Eligible)
	assert.False(t, readiness.Ready)
}

func TestModelReadinessUsesEligibleChunkRequirementForLoadLatency(t *testing.T) {
	cfg := config.NewFakeConfig()
	require.NoError(t, cfg.SetConfig(ECDFBaselineChunksConfigKey, "2"))
	store := ecdf.NewFakeChunkStore()
	service := &config.Service{Id: 3, Generation: 1, Interval: time.Minute}
	timestamp := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	chunk, err := ecdf.Encode(timestamp, []ecdf.Sample{{Value: 1, Count: 1}}, []ecdf.Sample{{Value: 10, Count: 1}})
	require.NoError(t, err)
	require.NoError(t, store.WriteChunk(service.Id, LoadLatencyIndicator, service.Generation, timestamp, chunk))

	readiness, err := ReadModelReadiness(context.Background(), cfg, store, service, LoadLatencyIndicator)
	require.NoError(t, err)
	assert.Equal(t, 0.5, readiness.Coverage)
	assert.Equal(t, 1, readiness.Eligible)
	assert.Equal(t, 2, readiness.Required)
	assert.False(t, readiness.Ready)
}
