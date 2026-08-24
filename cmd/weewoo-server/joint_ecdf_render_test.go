// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

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

type observedDoneContext struct {
	context.Context
	observed chan struct{}
}

func (c *observedDoneContext) Done() <-chan struct{} {
	select {
	case c.observed <- struct{}{}:
	default:
	}
	return c.Context.Done()
}

func TestJointECDFRenderCoordinatorSharedRenderSurvivesLeaderCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	coordinator := newJointECDFRenderCoordinator(
		func(ctx context.Context, _ []byte, width, height int, _ ecdf.RenderOptions) (*ecdf.RenderResponse, error) {
			calls.Add(1)
			close(started)
			select {
			case <-release:
				return successfulJointRender(width, height), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
		1, time.Second, 1024,
	)

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderResult := make(chan error, 1)
	go func() {
		_, err := coordinator.Render(leaderCtx, "same", []byte("definition"), 2, 2, 0)
		leaderResult <- err
	}()
	<-started

	followerCtx := &observedDoneContext{Context: context.Background(), observed: make(chan struct{}, 1)}
	followerResult := make(chan error, 1)
	go func() {
		_, err := coordinator.Render(followerCtx, "same", []byte("definition"), 2, 2, 0)
		followerResult <- err
	}()
	<-followerCtx.observed

	cancelLeader()
	assert.ErrorIs(t, <-leaderResult, context.Canceled)
	close(release)
	require.NoError(t, <-followerResult)
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
