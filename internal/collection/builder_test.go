package collection

import (
	"bytes"
	"context"
	"flag"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

type recordingJointStore struct {
	mu          sync.Mutex
	body        []byte
	serviceID   int
	indicatorID int
	intervalEnd time.Time
	intervals   map[time.Time]struct{}
	buildCount  int
	published   chan struct{}
}

func newRecordingJointStore() *recordingJointStore {
	return &recordingJointStore{
		intervals: make(map[time.Time]struct{}),
		published: make(chan struct{}, 1),
	}
}

func (s *recordingJointStore) Publish(ctx context.Context, serviceID, indicatorID int, intervalEnd time.Time, build func(io.Writer) error) (int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.intervals[intervalEnd]; exists {
		return 0, false, nil
	}
	var body bytes.Buffer
	if err := build(&body); err != nil {
		return 0, false, err
	}
	s.body = append([]byte(nil), body.Bytes()...)
	s.serviceID = serviceID
	s.indicatorID = indicatorID
	s.intervalEnd = intervalEnd
	s.intervals[intervalEnd] = struct{}{}
	s.buildCount++
	select {
	case s.published <- struct{}{}:
	default:
	}
	return int64(body.Len()), true, nil
}

func TestStartECDFBuilderBuildsIntervalOnceAcrossReplicas(t *testing.T) {
	setFakeJECDF(t, "#!/bin/sh\ncat >/dev/null\necho -n 'fake-ecdf-output'\n")
	cfg := config.NewFakeConfig()
	require.NoError(t, cfg.WriteService(&config.Service{Id: 7, Name: "api"}))
	joint := newRecordingJointStore()
	schedulerA := NewIntervalScheduler(WithSchedulerEventHandler(nil))
	schedulerB := NewIntervalScheduler(WithSchedulerEventHandler(nil))
	defer schedulerA.Stop()
	defer schedulerB.Stop()

	require.NoError(t, StartECDFBuilder(ecdf.NewFakeChunkStore(), joint, cfg, schedulerA))
	require.NoError(t, StartECDFBuilder(ecdf.NewFakeChunkStore(), joint, cfg, schedulerB))
	select {
	case <-joint.published:
	case <-time.After(time.Second):
		t.Fatal("scheduled ECDF was not published")
	}
	time.Sleep(50 * time.Millisecond)

	joint.mu.Lock()
	defer joint.mu.Unlock()
	assert.Equal(t, 1, joint.buildCount)
	assert.Len(t, joint.intervals, 1)
}

func (s *recordingJointStore) ReadCurrent(context.Context, int, int) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.body...), nil
}

func setFakeJECDF(t *testing.T, script string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jecdf")
	require.NoError(t, os.WriteFile(path, []byte(script), 0755))
	old := flag.Lookup("jecdf").Value.String()
	require.NoError(t, flag.Set("jecdf", path))
	t.Cleanup(func() { require.NoError(t, flag.Set("jecdf", old)) })
}

func TestBuildJointECDF(t *testing.T) {
	setFakeJECDF(t, "#!/bin/sh\ncat >/dev/null\necho -n 'fake-ecdf-output'\n")
	timestamp := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	chunk, err := ecdf.Encode(timestamp, []ecdf.Sample{{Value: 1, Count: 1}}, []ecdf.Sample{{Value: 2, Count: 1}})
	require.NoError(t, err)
	chunks := ecdf.NewFakeChunkStore()
	require.NoError(t, chunks.WriteChunk(1, LoadLatencyIndicator, timestamp, chunk))

	var out bytes.Buffer
	require.NoError(t, ecdf.BuildJointECDFContext(context.Background(), chunks, 1, LoadLatencyIndicator, &out))
	assert.Equal(t, "fake-ecdf-output", out.String())
}

func TestBuildECDFUsesContext(t *testing.T) {
	setFakeJECDF(t, "#!/bin/sh\nsleep 10\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := ecdf.BuildJointECDFContext(ctx, ecdf.NewFakeChunkStore(), 1, LoadLatencyIndicator, &bytes.Buffer{})
	require.ErrorIs(t, err, context.Canceled)
}

func TestStartECDFBuilderPublishesConfiguredServices(t *testing.T) {
	setFakeJECDF(t, "#!/bin/sh\ncat >/dev/null\necho -n 'fake-ecdf-output'\n")
	cfg := config.NewFakeConfig()
	require.NoError(t, cfg.WriteService(&config.Service{Id: 7, Name: "api"}))
	chunks := ecdf.NewFakeChunkStore()
	chunkTime := time.Now().Add(-30 * time.Minute)
	chunk, err := ecdf.Encode(chunkTime, []ecdf.Sample{{Value: 1, Count: 1}}, []ecdf.Sample{{Value: 2, Count: 1}})
	require.NoError(t, err)
	require.NoError(t, chunks.WriteChunk(7, LoadLatencyIndicator, chunkTime, chunk))
	joint := newRecordingJointStore()
	scheduler := NewIntervalScheduler(WithSchedulerEventHandler(nil))
	defer scheduler.Stop()

	require.NoError(t, StartECDFBuilder(chunks, joint, cfg, scheduler))
	select {
	case <-joint.published:
	case <-time.After(time.Second):
		t.Fatal("scheduled ECDF was not published")
	}
	joint.mu.Lock()
	defer joint.mu.Unlock()
	assert.Equal(t, 7, joint.serviceID)
	assert.Equal(t, LoadLatencyIndicator, joint.indicatorID)
	assert.Equal(t, "fake-ecdf-output", string(joint.body))
	assert.Equal(t, joint.intervalEnd.Truncate(serviceInterval), joint.intervalEnd)
}

func TestECDFPublisherDisabledSkipsScheduling(t *testing.T) {
	cfg := config.NewFakeConfig()
	require.NoError(t, cfg.WriteService(&config.Service{Id: 7, Name: "api"}))
	joint := newRecordingJointStore()
	scheduler := NewIntervalScheduler(WithSchedulerEventHandler(nil))
	defer scheduler.Stop()
	require.NoError(t, StartECDFBuilder(ecdf.NewFakeChunkStore(), joint, cfg, scheduler))
	select {
	case <-joint.published:
		t.Fatal("disabled publisher ran")
	case <-time.After(50 * time.Millisecond):
	}
}
