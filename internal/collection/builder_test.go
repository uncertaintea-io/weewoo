package collection

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

func TestBuildJointECDF(t *testing.T) {
	jecdfPath := filepath.Join(t.TempDir(), "jecdf")
	err := os.WriteFile(jecdfPath, []byte(`#!/bin/sh
if [ "$1" != "build" ]; then
	exit 2
fi
cat >/dev/null
echo -n 'fake-ecdf-output'
`), 0755)
	require.NoError(t, err)

	oldJECDF := flag.Lookup("jecdf").Value.String()
	require.NoError(t, flag.Set("jecdf", jecdfPath))
	t.Cleanup(func() {
		require.NoError(t, flag.Set("jecdf", oldJECDF))
	})

	timestamp := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	chunk, err := ecdf.Encode(timestamp, []ecdf.Sample{{Value: 1, Count: 1}}, []ecdf.Sample{{Value: 2, Count: 1}})
	require.NoError(t, err)

	chunkStore := ecdf.NewFakeChunkStore()
	require.NoError(t, chunkStore.WriteChunk(1, LoadLatencyIndicator, timestamp, chunk))

	var out bytes.Buffer
	err = ecdf.BuildJointECDFContext(context.Background(), chunkStore, 1, LoadLatencyIndicator, &out)
	require.NoError(t, err)
	assert.Equal(t, "fake-ecdf-output", out.String())
}

func TestBuildECDFUsesContext(t *testing.T) {
	jecdfPath := filepath.Join(t.TempDir(), "jecdf")
	err := os.WriteFile(jecdfPath, []byte(`#!/bin/sh
sleep 10
`), 0755)
	require.NoError(t, err)

	oldJECDF := flag.Lookup("jecdf").Value.String()
	require.NoError(t, flag.Set("jecdf", jecdfPath))
	t.Cleanup(func() {
		require.NoError(t, flag.Set("jecdf", oldJECDF))
	})

	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	chunk, err := ecdf.Encode(start.Add(time.Minute), []ecdf.Sample{{Value: 1, Count: 1}}, []ecdf.Sample{{Value: 2, Count: 1}})
	require.NoError(t, err)

	chunkStore := ecdf.NewFakeChunkStore()
	require.NoError(t, chunkStore.WriteChunk(1, LoadLatencyIndicator, start.Add(time.Minute), chunk))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var out bytes.Buffer
	err = ecdf.BuildJointECDFContext(ctx, chunkStore, 1, LoadLatencyIndicator, &out)
	require.ErrorIs(t, err, context.Canceled)
}

func TestStartECDFBuilderBuildsConfiguredServices(t *testing.T) {
	jecdfPath := filepath.Join(t.TempDir(), "jecdf")
	err := os.WriteFile(jecdfPath, []byte(`#!/bin/sh
if [ "$1" != "build" ]; then
	exit 2
fi
cat >/dev/null
echo -n 'fake-ecdf-output'
`), 0755)
	require.NoError(t, err)

	oldJECDF := flag.Lookup("jecdf").Value.String()
	require.NoError(t, flag.Set("jecdf", jecdfPath))
	t.Cleanup(func() {
		require.NoError(t, flag.Set("jecdf", oldJECDF))
	})

	cfg := config.NewFakeConfig()
	require.NoError(t, cfg.WriteService(&config.Service{Id: 7, Name: "api"}))
	outputRoot := t.TempDir()
	require.NoError(t, cfg.SetConfig(ECDFOutputDirConfigKey, outputRoot))

	chunkStore := ecdf.NewFakeChunkStore()
	chunkTime := time.Now().Add(-30 * time.Minute)
	chunk, err := ecdf.Encode(chunkTime, []ecdf.Sample{{Value: 1, Count: 1}}, []ecdf.Sample{{Value: 2, Count: 1}})
	require.NoError(t, err)
	require.NoError(t, chunkStore.WriteChunk(7, LoadLatencyIndicator, chunkTime, chunk))

	scheduler := NewIntervalScheduler(WithSchedulerEventHandler(nil))
	defer scheduler.Stop()

	require.NoError(t, StartECDFBuilder(chunkStore, cfg, scheduler))

	fileStore := newJointECDFFileStore(outputRoot, 7, LoadLatencyIndicator)
	require.Eventually(t, func() bool {
		got, err := ReadCurrentJointECDF(outputRoot, 7, LoadLatencyIndicator)
		return err == nil && string(got) == "fake-ecdf-output"
	}, time.Second, 10*time.Millisecond)
	_, err = os.Stat(fileStore.recoveryPath())
	assert.True(t, errors.Is(err, os.ErrNotExist))
}

func TestStartECDFBuilderDoesNotReplaceOutputWhenBuildFails(t *testing.T) {
	jecdfPath := filepath.Join(t.TempDir(), "jecdf")
	markerPath := filepath.Join(t.TempDir(), "failed-build")
	err := os.WriteFile(jecdfPath, []byte(fmt.Sprintf(`#!/bin/sh
touch %q
echo -n 'partial-output'
exit 1
`, markerPath)), 0755)
	require.NoError(t, err)

	oldJECDF := flag.Lookup("jecdf").Value.String()
	require.NoError(t, flag.Set("jecdf", jecdfPath))
	t.Cleanup(func() {
		require.NoError(t, flag.Set("jecdf", oldJECDF))
	})

	cfg := config.NewFakeConfig()
	require.NoError(t, cfg.WriteService(&config.Service{Id: 7, Name: "api"}))
	outputRoot := t.TempDir()
	require.NoError(t, cfg.SetConfig(ECDFOutputDirConfigKey, outputRoot))

	fileStore := newJointECDFFileStore(outputRoot, 7, LoadLatencyIndicator)
	_, err = fileStore.publish(func(w io.Writer) error {
		_, err := w.Write([]byte("existing-output"))
		return err
	})
	require.NoError(t, err)

	chunkStore := ecdf.NewFakeChunkStore()
	chunkTime := time.Now().Add(-30 * time.Minute)
	chunk, err := ecdf.Encode(chunkTime, []ecdf.Sample{{Value: 1, Count: 1}}, []ecdf.Sample{{Value: 2, Count: 1}})
	require.NoError(t, err)
	require.NoError(t, chunkStore.WriteChunk(7, LoadLatencyIndicator, chunkTime, chunk))

	scheduler := NewIntervalScheduler(WithSchedulerEventHandler(nil))
	defer scheduler.Stop()

	require.NoError(t, StartECDFBuilder(chunkStore, cfg, scheduler))

	require.Eventually(t, func() bool {
		_, err := os.Stat(markerPath)
		return err == nil
	}, time.Second, 10*time.Millisecond)
	got, err := ReadCurrentJointECDF(outputRoot, 7, LoadLatencyIndicator)
	require.NoError(t, err)
	assert.Equal(t, "existing-output", string(got))
}

func TestJointECDFFileStoreRecoverPromotesRecoveryManifest(t *testing.T) {
	fileStore := newJointECDFFileStore(t.TempDir(), 7, LoadLatencyIndicator)
	_, err := fileStore.publish(func(w io.Writer) error {
		_, err := w.Write([]byte("old-output"))
		return err
	})
	require.NoError(t, err)
	written, err := fileStore.writeRecovery(func(w io.Writer) error {
		_, err := w.Write([]byte("good-output"))
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, int64(len("good-output")), written)

	require.NoError(t, fileStore.recover())
	got, err := fileStore.readCurrent()
	require.NoError(t, err)
	assert.Equal(t, "good-output", string(got))
}

func TestJointECDFFileStoreReadCurrentFallsBackToPreviousWhenCurrentIsCorrupt(t *testing.T) {
	fileStore := newJointECDFFileStore(t.TempDir(), 7, LoadLatencyIndicator)
	_, err := fileStore.publish(func(w io.Writer) error {
		_, err := w.Write([]byte("previous-output"))
		return err
	})
	require.NoError(t, err)
	_, err = fileStore.publish(func(w io.Writer) error {
		_, err := w.Write([]byte("current-output"))
		return err
	})
	require.NoError(t, err)

	manifest, err := fileStore.readManifest(fileStore.manifestPath())
	require.NoError(t, err)
	require.NotNil(t, manifest.Current)
	require.NoError(t, os.WriteFile(filepath.Join(fileStore.dir, manifest.Current.File), []byte("corrupt"), 0644))

	got, err := fileStore.readCurrent()
	require.NoError(t, err)
	assert.Equal(t, "previous-output", string(got))
}

func TestJointECDFFileStoreCleansUpOldVersions(t *testing.T) {
	fileStore := newJointECDFFileStore(t.TempDir(), 7, LoadLatencyIndicator)
	for i := 0; i < ecdfRetainedVersions+3; i++ {
		body := fmt.Sprintf("output-%d", i)
		_, err := fileStore.publish(func(w io.Writer) error {
			_, err := w.Write([]byte(body))
			return err
		})
		require.NoError(t, err)
	}

	versions, err := fileStore.versionFiles()
	require.NoError(t, err)
	assert.Len(t, versions, ecdfRetainedVersions)
	got, err := fileStore.readCurrent()
	require.NoError(t, err)
	assert.Equal(t, "output-7", string(got))
}
