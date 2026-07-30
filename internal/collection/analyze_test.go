package collection

import (
	"context"
	"io"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/uncertaintea-io/weewoo/internal/alerting"
	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

type unreadJointStore struct{}

func (unreadJointStore) Publish(context.Context, int, int, time.Time, func(io.Writer) error) (int64, bool, error) {
	panic("unexpected Publish call")
}

func (unreadJointStore) ReadCurrent(context.Context, int, int) ([]byte, error) {
	panic("unexpected ReadCurrent call")
}

type staticJointStore struct{}

func (staticJointStore) Publish(context.Context, int, int, time.Time, func(io.Writer) error) (int64, bool, error) {
	panic("unexpected Publish call")
}

func (staticJointStore) ReadCurrent(context.Context, int, int) ([]byte, error) {
	return []byte("no published points"), nil
}

type recordingAlertQueue struct {
	mu        sync.Mutex
	outcomes  []alerting.AnalysisOutcome
	failures  []alerting.AnalysisOutcome
	baselines int
}

type recordedVerdict struct {
	serviceID   int
	indicatorID int
	timestamp   time.Time
	good        bool
	pValue      float64
}

type recordingChunkStore struct {
	ecdf.ChunkStore
	verdicts []recordedVerdict
}

func (s *recordingChunkStore) WriteVerdict(_ context.Context, serviceID, indicatorID int, _ int64, timestamp time.Time, good bool, pValue float64) error {
	s.verdicts = append(s.verdicts, recordedVerdict{
		serviceID:   serviceID,
		indicatorID: indicatorID,
		timestamp:   timestamp,
		good:        good,
		pValue:      pValue,
	})
	return nil
}

func (q *recordingAlertQueue) RecordAnalysis(_ context.Context, outcome alerting.AnalysisOutcome) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.outcomes = append(q.outcomes, outcome)
	return nil
}

func (q *recordingAlertQueue) RecordBaseline(context.Context, int, int, time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.baselines++
	return nil
}

func (q *recordingAlertQueue) RecordAnalysisFailure(_ context.Context, outcome alerting.AnalysisOutcome, _ error) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.failures = append(q.failures, outcome)
	return nil
}

func (q *recordingAlertQueue) Count() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	count := 0
	for _, outcome := range q.outcomes {
		if outcome.Anomalous {
			count++
		}
	}
	return count
}

func TestAnalyseSampleRejectsOverflowingSampleCount(t *testing.T) {
	const serviceID, indicatorID = 1, 2
	timestamp := time.Unix(1_700_000_000, 0)
	loads := []ecdf.Sample{{Value: 12, Count: 1}}
	latencies := []ecdf.Sample{{Value: 30, Count: math.MaxUint64}, {Value: 31, Count: 1}}

	_, err := analyzeSample(context.Background(), config.NewFakeConfig(), unreadJointStore{}, nil, nil, &config.Service{Id: serviceID, Name: "test"}, indicatorID, timestamp, loads, latencies)
	require.EqualError(t, err, "invalid latency samples: observation count overflows uint64")
}

func TestAnalyseSampleRejectsOverflowingLoadCount(t *testing.T) {
	const serviceID, indicatorID = 1, 2
	timestamp := time.Unix(1_700_000_000, 0)
	loads := []ecdf.Sample{{Value: 12, Count: math.MaxUint64}, {Value: 13, Count: 1}}
	latencies := []ecdf.Sample{{Value: 30, Count: 1}}

	_, err := analyzeSample(context.Background(), config.NewFakeConfig(), unreadJointStore{}, nil, nil, &config.Service{Id: serviceID, Name: "test"}, indicatorID, timestamp, loads, latencies)
	require.EqualError(t, err, "invalid load samples: observation count overflows uint64")
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
			_, err := analyzeSample(context.Background(), config.NewFakeConfig(), unreadJointStore{}, nil, nil, &config.Service{Id: serviceID, Name: "test"}, indicatorID, timestamp, test.loads, test.latencies)
			require.EqualError(t, err, test.wantError)
		})
	}
}

func TestIsStatisticallySignificantUsesPValueDirectly(t *testing.T) {
	require.Equal(t, 0.01, ksSignificanceLevel)

	tests := []struct {
		name        string
		pValue      float64
		significant bool
	}{
		{"high p-value is not significant", 0.96, false},
		{"significance level is not significant", ksSignificanceLevel, false},
		{"below significance level is significant", 0.009, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.significant, isStatisticallySignificant(test.pValue))
		})
	}
}

func TestAnalyzeSampleSkipsAlertWhenQueryHasZeroPoints(t *testing.T) {
	setFakeJECDF(t, `#!/bin/sh
if [ "$1" != "query" ]; then
exit 2
fi
cat >/dev/null
printf '\000'
`)
	alerts := &recordingAlertQueue{}

	anomalous, err := analyzeSample(
		context.Background(),
		config.NewFakeConfig(),
		staticJointStore{},
		nil,
		alerts,
		&config.Service{Id: 1, Name: "test"},
		LoadLatencyIndicator,
		time.Unix(1_700_000_000, 0),
		[]ecdf.Sample{{Value: 0, Count: 1}},
		[]ecdf.Sample{{Value: 0, Count: 1}},
	)

	require.NoError(t, err)
	require.False(t, anomalous)
	require.Zero(t, alerts.Count())
}

func TestAnalyzeSampleSkipsSupersededServiceGeneration(t *testing.T) {
	cfg := config.NewFakeConfig()
	require.NoError(t, cfg.WriteService(&config.Service{Id: 7, Name: "checkout", Generation: 2}))
	alerts := &recordingAlertQueue{}

	anomalous, err := analyzeSample(
		context.Background(),
		cfg,
		staticJointStore{},
		nil,
		alerts,
		&config.Service{Id: 7, Name: "checkout", Generation: 1},
		LoadLatencyIndicator,
		time.Unix(1_700_000_000, 0),
		[]ecdf.Sample{{Value: 1, Count: 1}},
		[]ecdf.Sample{{Value: 1, Count: 1}},
	)

	require.NoError(t, err)
	require.False(t, anomalous)
	require.Zero(t, alerts.Count())
}

func TestAnalyzeSampleRecordsAnomalousOutcomeAtomically(t *testing.T) {
	setFakeJECDF(t, `#!/bin/sh
if [ "$1" != "query" ]; then
exit 2
fi
cat >/dev/null
printf '\002\000\000\000\000\000\000\000\000\000\000\000\000\000\000\000\000\077\360\000\000\000\000\000\000\077\360\000\000\000\000\000\000'
`)
	verdicts := &recordingChunkStore{}
	alerts := &recordingAlertQueue{}
	timestamp := time.Unix(1_700_000_000, 0)

	anomalous, err := analyzeSample(
		context.Background(),
		config.NewFakeConfig(),
		staticJointStore{},
		verdicts,
		alerts,
		&config.Service{Id: 7, Name: "checkout"},
		LoadLatencyIndicator,
		timestamp,
		[]ecdf.Sample{{Value: 0.5, Count: 10}},
		[]ecdf.Sample{{Value: 10, Count: 10}},
	)

	require.NoError(t, err)
	require.True(t, anomalous)
	require.Empty(t, verdicts.verdicts)
	require.Equal(t, 1, alerts.Count())
	require.Len(t, alerts.outcomes, 1)
	require.Equal(t, 7, alerts.outcomes[0].ServiceID)
	require.Equal(t, LoadLatencyIndicator, alerts.outcomes[0].IndicatorID)
	require.Equal(t, timestamp, alerts.outcomes[0].Timestamp)
	require.True(t, alerts.outcomes[0].Anomalous)
	require.Less(t, alerts.outcomes[0].PValue, ksSignificanceLevel)
}

func TestHistoricalAnomalyRecordsVerdictWithoutAlert(t *testing.T) {
	setFakeJECDF(t, `#!/bin/sh
if [ "$1" != "query" ]; then
exit 2
fi
cat >/dev/null
printf '\002\000\000\000\000\000\000\000\000\000\000\000\000\000\000\000\000\077\360\000\000\000\000\000\000\077\360\000\000\000\000\000\000'
`)
	verdicts := &recordingChunkStore{}
	alerts := &recordingAlertQueue{}
	timestamp := time.Unix(1_700_000_000, 0)

	anomalous, err := analyzeSample(
		context.Background(),
		config.NewFakeConfig(),
		staticJointStore{},
		verdicts,
		alerts,
		&config.Service{Id: 7, Name: "checkout"},
		LoadLatencyIndicator,
		timestamp,
		[]ecdf.Sample{{Value: 0.5, Count: 10}},
		[]ecdf.Sample{{Value: 10, Count: 10}},
		true,
	)

	require.NoError(t, err)
	require.True(t, anomalous)
	require.Empty(t, alerts.outcomes)
	require.Len(t, verdicts.verdicts, 1)
	require.False(t, verdicts.verdicts[0].good)
	require.Equal(t, timestamp, verdicts.verdicts[0].timestamp)
}

func TestAnalyzeSampleReplacesBadVerdictWhenReevaluationIsNormal(t *testing.T) {
	setFakeJECDF(t, `#!/bin/sh
if [ "$1" != "query" ]; then
exit 2
fi
cat >/dev/null
printf '\002\000\000\000\000\000\000\000\000\000\000\000\000\000\000\000\000\077\360\000\000\000\000\000\000\077\360\000\000\000\000\000\000'
`)
	timestamp := time.Unix(1_700_000_000, 0)
	verdicts := &recordingChunkStore{
		verdicts: []recordedVerdict{{
			serviceID:   7,
			indicatorID: LoadLatencyIndicator,
			timestamp:   timestamp,
			good:        false,
			pValue:      0,
		}},
	}

	anomalous, err := analyzeSample(
		context.Background(),
		config.NewFakeConfig(),
		staticJointStore{},
		verdicts,
		nil,
		&config.Service{Id: 7, Name: "checkout"},
		LoadLatencyIndicator,
		timestamp,
		[]ecdf.Sample{{Value: 0.5, Count: 10}},
		[]ecdf.Sample{
			{Value: 0.1, Count: 1},
			{Value: 0.2, Count: 1},
			{Value: 0.3, Count: 1},
			{Value: 0.4, Count: 1},
			{Value: 0.5, Count: 1},
			{Value: 0.6, Count: 1},
			{Value: 0.7, Count: 1},
			{Value: 0.8, Count: 1},
			{Value: 0.9, Count: 1},
			{Value: 1, Count: 1},
		},
	)

	require.NoError(t, err)
	require.False(t, anomalous)
	require.Len(t, verdicts.verdicts, 2)
	replacement := verdicts.verdicts[1]
	require.Equal(t, 7, replacement.serviceID)
	require.Equal(t, LoadLatencyIndicator, replacement.indicatorID)
	require.Equal(t, timestamp, replacement.timestamp)
	require.True(t, replacement.good)
	require.GreaterOrEqual(t, replacement.pValue, ksSignificanceLevel)
}
