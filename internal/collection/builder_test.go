// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

package collection

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
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

type unreadableServiceConfig struct {
	config.Config
}

type blockedJointStore struct {
	entered   chan struct{}
	release   chan struct{}
	published chan struct{}
}

func (s *blockedJointStore) Publish(ctx context.Context, _ int, _ int, _ time.Time, build func(io.Writer) error) (int64, bool, error) {
	close(s.entered)
	select {
	case <-s.release:
	case <-ctx.Done():
		return 0, false, ctx.Err()
	}
	var body bytes.Buffer
	if err := build(&body); err != nil {
		return 0, false, err
	}
	close(s.published)
	return int64(body.Len()), true, nil
}

func (*blockedJointStore) ReadCurrent(context.Context, int, int) ([]byte, string, error) {
	return nil, "", errors.New("not implemented")
}

func (unreadableServiceConfig) ReadService(int) (*config.Service, error) {
	return nil, errors.New("configuration temporarily unavailable")
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

func (s *recordingJointStore) ReadCurrent(context.Context, int, int) ([]byte, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.body...), "", nil
}

func TestStartECDFBuilderBuildsIntervalOnceAcrossReplicas(t *testing.T) {
	setFakeJECDF(t, "#!/bin/sh\ncat >/dev/null\necho -n 'fake-ecdf-output'\n")
	cfg := config.NewFakeConfig()
	require.NoError(t, cfg.WriteService(&config.Service{Id: 7, Name: "api"}))
	require.NoError(t, cfg.SetConfig(ECDFBaselineChunksConfigKey, "1"))
	chunks := ecdf.NewFakeChunkStore()
	chunkTime := time.Now().Add(-30 * time.Minute)
	chunk, err := ecdf.Encode(chunkTime, []ecdf.Sample{{Value: 1, Count: 1}}, []ecdf.Sample{{Value: 2, Count: 1}})
	require.NoError(t, err)
	require.NoError(t, chunks.WriteChunk(7, ecdf.LoadLatencyIndicator, 1, chunkTime, chunk))
	joint := newRecordingJointStore()
	schedulerA := NewIntervalScheduler(WithSchedulerEventHandler(nil))
	schedulerB := NewIntervalScheduler(WithSchedulerEventHandler(nil))
	defer schedulerA.Stop()
	defer schedulerB.Stop()

	require.NoError(t, StartECDFBuilder(chunks, joint, cfg, schedulerA))
	require.NoError(t, StartECDFBuilder(chunks, joint, cfg, schedulerB))
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
	require.NoError(t, chunks.WriteChunk(1, ecdf.LoadLatencyIndicator, 1, timestamp, chunk))

	var out bytes.Buffer
	require.NoError(t, ecdf.BuildJointECDF(context.Background(), chunks, 1, ecdf.LoadLatencyIndicator, 1, &out))
	assert.Equal(t, "fake-ecdf-output", out.String())
}

func TestBuildECDFUsesContext(t *testing.T) {
	setFakeJECDF(t, "#!/bin/sh\nsleep 10\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := ecdf.BuildJointECDF(ctx, ecdf.NewFakeChunkStore(), 1, ecdf.LoadLatencyIndicator, 1, &bytes.Buffer{})
	require.ErrorIs(t, err, context.Canceled)
}

func TestStartECDFBuilderPublishesConfiguredServices(t *testing.T) {
	setFakeJECDF(t, "#!/bin/sh\ncat >/dev/null\necho -n 'fake-ecdf-output'\n")
	cfg := config.NewFakeConfig()
	require.NoError(t, cfg.WriteService(&config.Service{Id: 7, Name: "api"}))
	require.NoError(t, cfg.SetConfig(ECDFBaselineChunksConfigKey, "1"))
	chunks := ecdf.NewFakeChunkStore()
	chunkTime := time.Now().Add(-30 * time.Minute)
	chunk, err := ecdf.Encode(chunkTime, []ecdf.Sample{{Value: 1, Count: 1}}, []ecdf.Sample{{Value: 2, Count: 1}})
	require.NoError(t, err)
	require.NoError(t, chunks.WriteChunk(7, ecdf.LoadLatencyIndicator, 1, chunkTime, chunk))
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
	assert.Equal(t, ecdf.LoadLatencyIndicator, joint.indicatorID)
	assert.Equal(t, "fake-ecdf-output", string(joint.body))
	assert.Equal(t, joint.intervalEnd.Truncate(serviceInterval), joint.intervalEnd)
}

func TestECDFBuilderDoesNotReuseChunksFromBeforeConfigurationChange(t *testing.T) {
	cfg := config.NewFakeConfig()
	resetAt := time.Now().UTC()
	require.NoError(t, cfg.WriteService(&config.Service{Id: 7, Name: "api", Generation: 2, BaselineResetAt: resetAt}))
	require.NoError(t, cfg.SetConfig(ECDFBaselineChunksConfigKey, "1"))
	chunks := ecdf.NewFakeChunkStore()
	oldTime := resetAt.Add(-time.Hour)
	chunk, err := ecdf.Encode(oldTime, []ecdf.Sample{{Value: 1, Count: 1}}, []ecdf.Sample{{Value: 2, Count: 1}})
	require.NoError(t, err)
	require.NoError(t, chunks.WriteChunk(7, ecdf.LoadLatencyIndicator, 1, oldTime, chunk))
	joint := newRecordingJointStore()
	scheduler := NewIntervalScheduler(WithSchedulerEventHandler(nil))
	defer scheduler.Stop()

	require.NoError(t, ScheduleECDFBuilder(7, chunks, joint, cfg, scheduler))

	select {
	case <-joint.published:
		t.Fatal("old-generation chunks produced a new baseline")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestECDFBuilderDoesNotPublishAfterServiceGenerationChanges(t *testing.T) {
	setFakeJECDF(t, "#!/bin/sh\ncat >/dev/null\necho -n 'old-generation-ecdf'\n")
	cfg := config.NewFakeConfig()
	service := &config.Service{Id: 7, Name: "api"}
	require.NoError(t, cfg.WriteService(service))
	require.NoError(t, cfg.SetConfig(ECDFBaselineChunksConfigKey, "1"))
	chunks := ecdf.NewFakeChunkStore()
	chunkTime := time.Now().Add(-30 * time.Minute)
	chunk, err := ecdf.Encode(chunkTime, []ecdf.Sample{{Value: 1, Count: 1}}, []ecdf.Sample{{Value: 2, Count: 1}})
	require.NoError(t, err)
	require.NoError(t, chunks.WriteChunk(7, ecdf.LoadLatencyIndicator, service.Generation, chunkTime, chunk))
	joint := &blockedJointStore{
		entered:   make(chan struct{}),
		release:   make(chan struct{}),
		published: make(chan struct{}),
	}
	scheduler := NewIntervalScheduler(WithSchedulerEventHandler(nil))
	defer scheduler.Stop()

	require.NoError(t, ScheduleECDFBuilder(7, chunks, joint, cfg, scheduler))
	select {
	case <-joint.entered:
	case <-time.After(time.Second):
		t.Fatal("publisher did not reach the advisory-lock boundary")
	}
	_, err = cfg.ResetServiceBaseline(context.Background(), service.Id, service.Revision, "test")
	require.NoError(t, err)
	close(joint.release)

	select {
	case <-joint.published:
		t.Fatal("old-generation ECDF was published after baseline reset")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestECDFBuilderRetriesWhenServiceGenerationCannotBeRead(t *testing.T) {
	cfg := config.NewFakeConfig()
	require.NoError(t, cfg.SetConfig(ECDFBaselineChunksConfigKey, "1"))
	chunks := ecdf.NewFakeChunkStore()
	chunkTime := time.Now().Add(-30 * time.Minute)
	chunk, err := ecdf.Encode(chunkTime, []ecdf.Sample{{Value: 1, Count: 1}}, []ecdf.Sample{{Value: 2, Count: 1}})
	require.NoError(t, err)
	require.NoError(t, chunks.WriteChunk(7, ecdf.LoadLatencyIndicator, 1, chunkTime, chunk))
	joint := newRecordingJointStore()
	events := make(chan SchedulerEvent, 8)
	scheduler := NewIntervalScheduler(WithSchedulerEventHandler(func(event SchedulerEvent) {
		events <- event
	}))
	defer scheduler.Stop()

	require.NoError(t, ScheduleECDFBuilder(7, chunks, joint, unreadableServiceConfig{Config: cfg}, scheduler))

	require.Eventually(t, func() bool {
		for {
			select {
			case event := <-events:
				if event.Kind == SchedulerEventRetryScheduled {
					return event.Err != nil && strings.Contains(event.Err.Error(), "configuration temporarily unavailable")
				}
			default:
				return false
			}
		}
	}, time.Second, time.Millisecond)
	select {
	case <-joint.published:
		t.Fatal("builder published without knowing the service generation")
	default:
	}
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

func TestCallbackID(t *testing.T) {
	tests := []struct {
		name         string
		serviceID    int
		callbackType CallbackType
		want         int
	}{
		{name: "first service collect callback", serviceID: 1, callbackType: CollectCallback, want: 1002},
		{name: "first service builder callback", serviceID: 1, callbackType: BuilderCallback, want: 1003},
		{name: "service IDs do not overlap", serviceID: 1001, callbackType: BuilderCallback, want: 3003},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CallbackID(tt.serviceID, tt.callbackType); got != tt.want {
				t.Fatalf("CallbackID(%d, %d) = %d, want %d", tt.serviceID, tt.callbackType, got, tt.want)
			}
		})
	}
}

func TestCollectionAndBuilderCallbacksUseDistinctIDsRegardlessOfRegistrationOrder(t *testing.T) {
	tests := []struct {
		name            string
		scheduleBuilder bool
	}{
		{name: "collection before builder"},
		{name: "builder before collection", scheduleBuilder: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var events []SchedulerEvent
			scheduler := NewIntervalScheduler(WithSchedulerEventHandler(func(event SchedulerEvent) {
				events = append(events, event)
			}))
			defer scheduler.Stop()

			cfg := config.NewFakeConfig()
			collector := NewCollector(nil, ecdf.NewFakeChunkStore(), scheduler, nil)
			service := &config.Service{Id: 1003, Name: "collision", Interval: time.Hour}
			scheduleCollection := func() { require.NoError(t, collector.Schedule(service)) }
			scheduleBuilder := func() {
				require.NoError(t, ScheduleECDFBuilder(1, ecdf.NewFakeChunkStore(), newRecordingJointStore(), cfg, scheduler))
			}

			if tt.scheduleBuilder {
				scheduleBuilder()
				scheduleCollection()
			} else {
				scheduleCollection()
				scheduleBuilder()
			}

			var added, updated []int
			for _, event := range events {
				switch event.Kind {
				case SchedulerEventCallbackAdded:
					added = append(added, event.ID)
				case SchedulerEventCallbackUpdated:
					updated = append(updated, event.ID)
				}
			}
			assert.ElementsMatch(t, []int{1003, 3006}, added)
			assert.Empty(t, updated)
		})
	}
}
