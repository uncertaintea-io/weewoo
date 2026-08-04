package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

func successfulJointRender(width, height int) *ecdf.RenderResponse {
	return &ecdf.RenderResponse{Width: width, Height: height, Masses: []float64{0.5}}
}

func TestJointECDFRenderCoordinatorCachesEncodedResponse(t *testing.T) {
	var calls atomic.Int32
	coordinator := newJointECDFRenderCoordinator(
		func(_ context.Context, _ []byte, width, height int, _ ecdf.RenderOptions) (*ecdf.RenderResponse, error) {
			calls.Add(1)
			return successfulJointRender(width, height), nil
		},
		1, time.Second, 1024,
	)

	first, err := coordinator.Render(context.Background(), "same", []byte("definition"), 2, 2, 0)
	require.NoError(t, err)
	second, err := coordinator.Render(context.Background(), "same", []byte("definition"), 2, 2, 0)
	require.NoError(t, err)

	assert.Equal(t, first, second)
	assert.Equal(t, int32(1), calls.Load())
}

func TestJointECDFRenderCoordinatorCoalescesConcurrentRequests(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	coordinator := newJointECDFRenderCoordinator(
		func(_ context.Context, _ []byte, width, height int, _ ecdf.RenderOptions) (*ecdf.RenderResponse, error) {
			calls.Add(1)
			close(started)
			<-release
			return successfulJointRender(width, height), nil
		},
		1, time.Second, 1024,
	)

	results := make(chan error, 2)
	go func() {
		_, err := coordinator.Render(context.Background(), "same", []byte("definition"), 2, 2, 0)
		results <- err
	}()
	<-started
	go func() {
		_, err := coordinator.Render(context.Background(), "same", []byte("definition"), 2, 2, 0)
		results <- err
	}()
	close(release)

	require.NoError(t, <-results)
	require.NoError(t, <-results)
	assert.Equal(t, int32(1), calls.Load())
}

func TestJointECDFRenderCoordinatorRejectsDistinctWorkAtCapacity(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	coordinator := newJointECDFRenderCoordinator(
		func(_ context.Context, _ []byte, width, height int, _ ecdf.RenderOptions) (*ecdf.RenderResponse, error) {
			close(started)
			<-release
			return successfulJointRender(width, height), nil
		},
		1, time.Second, 1024,
	)

	first := make(chan error, 1)
	go func() {
		_, err := coordinator.Render(context.Background(), "first", []byte("first"), 2, 2, 0)
		first <- err
	}()
	<-started
	_, err := coordinator.Render(context.Background(), "second", []byte("second"), 2, 2, 0)
	assert.ErrorIs(t, err, errJointECDFRenderBusy)
	close(release)
	require.NoError(t, <-first)
}

func TestJointECDFRenderCoordinatorTimesOutRender(t *testing.T) {
	coordinator := newJointECDFRenderCoordinator(
		func(ctx context.Context, _ []byte, _, _ int, _ ecdf.RenderOptions) (*ecdf.RenderResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		1, time.Millisecond, 1024,
	)

	_, err := coordinator.Render(context.Background(), "timeout", []byte("definition"), 2, 2, 0)
	assert.True(t, errors.Is(err, context.DeadlineExceeded))
}

func TestJointECDFRenderCacheEvictsLeastRecentlyUsedEntry(t *testing.T) {
	cache := newJointECDFRenderCache(6)
	cache.Add("first", []byte("123"))
	cache.Add("second", []byte("456"))
	_, ok := cache.Get("first")
	require.True(t, ok)
	cache.Add("third", []byte("789"))

	_, firstOK := cache.Get("first")
	_, secondOK := cache.Get("second")
	_, thirdOK := cache.Get("third")
	assert.True(t, firstOK)
	assert.False(t, secondOK)
	assert.True(t, thirdOK)
}
