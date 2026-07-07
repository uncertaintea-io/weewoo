package ecdf

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFakeJECDF(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jecdf")
	err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0755)
	require.NoError(t, err)
	return path
}

func setJECDFTool(t *testing.T, path string) {
	t.Helper()
	old := *jecdf
	*jecdf = path
	t.Cleanup(func() {
		*jecdf = old
	})
}

func jecdfExists(t *testing.T) bool {
	t.Helper()
	info, err := os.Stat(*jecdf)
	if err != nil {
		t.Log(err)
		return false
	}

	// Ensure it is a regular file and not a directory
	if info.IsDir() {
		t.Log("jecdf is a directory, not a file")
		return false
	}

	// Check if any execution bits (User, Group, or Other) are set
	if info.Mode().Perm()&0111 != 0 {
		return true
	}

	t.Log("file exists but is not executable")
	return false
}

func newTestChunk(t time.Time, x, y float64) ([]byte, error) {
	return Encode(t, []Sample{{x, 1}}, []Sample{{y, 1}})
}

func TestBuildWithRealTool(t *testing.T) {
	setJECDFTool(t, "../../jecdf")
	if  !jecdfExists(t) {
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
	err = store.WriteChunk(serviceId, indicatorId, t1, chunk)
	require.NoError(t, err)

	chunk2, err := newTestChunk(t2, 2, 1)
	require.NoError(t, err)
	err = store.WriteChunk(serviceId, indicatorId, t2, chunk2)
	require.NoError(t, err)

	out, err := BuildJointECDF(store, serviceId, indicatorId, t1.Add(-time.Second), t2.Add(time.Second))
	require.NoError(t, err)
	assert.NotNil(t, out)
	assert.Greater(t, len(out), 20)
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
	err = store.WriteChunk(serviceId, indicatorId, t1, chunk)
	require.NoError(t, err)

	chunk2, err := newTestChunk(t2, 2, 1)
	require.NoError(t, err)
	err = store.WriteChunk(serviceId, indicatorId, t2, chunk2)
	require.NoError(t, err)

	out, err := BuildJointECDF(store, serviceId, indicatorId, t1.Add(-time.Second), t2.Add(time.Second))
	require.NoError(t, err)
	assert.NotNil(t, out)
	assert.Equal(t, "fake-ecdf-output", string(out))
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
		require.NoError(t, store.WriteChunk(serviceId, indicatorId, start.Add(time.Duration(i)*time.Second), chunk))
	}

	done := make(chan error, 1)
	go func() {
		_, err := BuildJointECDF(store, serviceId, indicatorId, start.Add(-time.Second), start.Add(10*time.Second))
		done <- err
	}()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to build joint ECDF")
	case <-time.After(2 * time.Second):
		t.Fatal("BuildJointECDF did not return after jecdf exited")
	}
}
