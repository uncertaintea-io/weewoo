// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

package ecdf

import (
	"bytes"
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func newTestChunk(t time.Time, x, y float64) ([]byte, error) {
	return Encode(t, []Sample{{x, 1}}, []Sample{{y, 1}})
}

func TestBuildWithRealTool(t *testing.T) {
	setJECDFTool(t, "../../jecdf")
	if !jecdfExists(t) {
		t.Skip("jecdf tool not found or not executable, skipping test")
		return
	}
	const (
		serviceId   = 1
		indicatorId = 1
	)
	store := NewFakeChunkStore()
	require.NotNil(t, store)
	t2 := time.Now()
	t1 := t2.Add(-time.Minute)

	chunk, err := newTestChunk(t1, 1, 2)
	require.NoError(t, err)
	err = store.WriteChunk(serviceId, indicatorId, 1, t1, chunk)
	require.NoError(t, err)

	chunk2, err := newTestChunk(t2, 2, 1)
	require.NoError(t, err)
	err = store.WriteChunk(serviceId, indicatorId, 1, t2, chunk2)
	require.NoError(t, err)

	var out bytes.Buffer
	err = BuildJointECDF(context.Background(), store, serviceId, indicatorId, 1, &out)
	require.NoError(t, err)
	assert.NotNil(t, out)
	assert.Greater(t, out.Len(), 20)
}

func TestBuildWithFakeTool(t *testing.T) {
	const (
		serviceId   = 1
		indicatorId = 1
	)
	store := NewFakeChunkStore()
	require.NotNil(t, store)
	setJECDFTool(t, writeFakeJECDF(t, `if [ "$1" != "build" ]; then
	exit 2
fi
cat >/dev/null
echo -n 'fake-ecdf-output'
`))

	t2 := time.Now()
	t1 := t2.Add(-time.Minute)

	chunk, err := newTestChunk(t1, 1, 2)
	require.NoError(t, err)
	err = store.WriteChunk(serviceId, indicatorId, 1, t1, chunk)
	require.NoError(t, err)

	chunk2, err := newTestChunk(t2, 2, 1)
	require.NoError(t, err)
	err = store.WriteChunk(serviceId, indicatorId, 1, t2, chunk2)
	require.NoError(t, err)

	var out bytes.Buffer
	err = BuildJointECDF(context.Background(), store, serviceId, indicatorId, 1, &out)
	require.NoError(t, err)
	assert.NotNil(t, out)
	assert.Equal(t, "fake-ecdf-output", out.String())
}

func TestBuildReturnsWhenToolStopsConsuming(t *testing.T) {
	const (
		serviceId   = 1
		indicatorId = 1
	)
	store := NewFakeChunkStore()
	setJECDFTool(t, writeFakeJECDF(t, "exit 1\n"))

	start := time.Now()
	for i := range 8 {
		chunk, err := newTestChunk(start.Add(time.Duration(i)*time.Second), float64(i), float64(i+1))
		require.NoError(t, err)
		require.NoError(t, store.WriteChunk(serviceId, indicatorId, 1, start.Add(time.Duration(i)*time.Second), chunk))
	}

	done := make(chan error, 1)
	go func() {
		var out bytes.Buffer
		err := BuildJointECDF(context.Background(), store, serviceId, indicatorId, 1, &out)
		done <- err
	}()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "jecdf failed")
	case <-time.After(2 * time.Second):
		t.Fatal("BuildJointECDF did not return after jecdf exited")
	}
}
