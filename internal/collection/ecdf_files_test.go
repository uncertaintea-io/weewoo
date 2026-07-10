package collection

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManifestLockContentionTimesOutAndClosesDescriptor(t *testing.T) {
	store := newJointECDFFileStore(t.TempDir(), 1, 2)
	store.lockWaitTimeout = 40 * time.Millisecond
	require.NoError(t, os.MkdirAll(store.dir, 0755))

	owner, err := store.acquireManifestLock(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, countOpenDescriptorsForPath(t, store.lockPath()))

	started := time.Now()
	_, err = store.acquireManifestLock(context.Background())
	require.ErrorIs(t, err, errECDFManifestLockTimeout)
	require.Less(t, time.Since(started), time.Second)
	require.Equal(t, 1, countOpenDescriptorsForPath(t, store.lockPath()))

	require.NoError(t, store.releaseManifestLock(owner))
	require.Equal(t, 0, countOpenDescriptorsForPath(t, store.lockPath()))
}

func TestManifestLockWaitHonorsContextCancellation(t *testing.T) {
	store := newJointECDFFileStore(t.TempDir(), 1, 2)
	store.lockWaitTimeout = time.Second
	require.NoError(t, os.MkdirAll(store.dir, 0755))
	owner, err := store.acquireManifestLock(context.Background())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := store.acquireManifestLock(ctx)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, errECDFManifestLockCanceled)
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("lock waiter did not stop after cancellation")
	}
	require.Equal(t, 1, countOpenDescriptorsForPath(t, store.lockPath()))
	require.NoError(t, store.releaseManifestLock(owner))
}

func TestManifestLockWaiterAcquiresAfterRelease(t *testing.T) {
	store := newJointECDFFileStore(t.TempDir(), 1, 2)
	store.lockWaitTimeout = time.Second
	require.NoError(t, os.MkdirAll(store.dir, 0755))
	owner, err := store.acquireManifestLock(context.Background())
	require.NoError(t, err)

	next := make(chan *jointECDFManifestLock, 1)
	errs := make(chan error, 1)
	go func() {
		lock, err := store.acquireManifestLock(context.Background())
		next <- lock
		errs <- err
	}()
	time.Sleep(25 * time.Millisecond)
	require.NoError(t, store.releaseManifestLock(owner))
	require.NoError(t, <-errs)
	require.NoError(t, store.releaseManifestLock(<-next))
}

func TestWithManifestLockReleasesAfterCallbackError(t *testing.T) {
	store := newJointECDFFileStore(t.TempDir(), 1, 2)
	primary := errors.New("callback failed")
	require.ErrorIs(t, store.withManifestLock(context.Background(), func() error { return primary }), primary)

	require.NoError(t, store.withManifestLock(context.Background(), func() error { return nil }))
}

func TestWithManifestLockReleasesAfterCallbackPanic(t *testing.T) {
	store := newJointECDFFileStore(t.TempDir(), 1, 2)
	func() {
		defer func() { require.Equal(t, "boom", recover()) }()
		_ = store.withManifestLock(context.Background(), func() error { panic("boom") })
	}()

	require.NoError(t, store.withManifestLock(context.Background(), func() error { return nil }))
}

func TestPublishPanicRemovesIncompleteOutputAndPreservesCurrent(t *testing.T) {
	store := newJointECDFFileStore(t.TempDir(), 1, 2)
	_, err := store.publish(context.Background(), func(w io.Writer) error {
		_, err := w.Write([]byte("last-known-good"))
		return err
	})
	require.NoError(t, err)
	manifestBefore, err := os.ReadFile(store.manifestPath())
	require.NoError(t, err)

	func() {
		defer func() { require.Equal(t, "build panic", recover()) }()
		_, _ = store.publish(context.Background(), func(w io.Writer) error {
			_, err := w.Write([]byte("partial"))
			require.NoError(t, err)
			panic("build panic")
		})
	}()

	temps, err := filepath.Glob(filepath.Join(store.dir, ecdfUploadFile+"-*"))
	require.NoError(t, err)
	require.Empty(t, temps)
	manifestAfter, err := os.ReadFile(store.manifestPath())
	require.NoError(t, err)
	require.Equal(t, manifestBefore, manifestAfter)
	body, err := store.readCurrent(context.Background())
	require.NoError(t, err)
	require.Equal(t, "last-known-good", string(body))
}

func TestCanceledPublishRemovesIncompleteOutputAndPreservesCurrent(t *testing.T) {
	store := newJointECDFFileStore(t.TempDir(), 1, 2)
	_, err := store.publish(context.Background(), func(w io.Writer) error {
		_, err := w.Write([]byte("last-known-good"))
		return err
	})
	require.NoError(t, err)
	manifestBefore, err := os.ReadFile(store.manifestPath())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := store.publish(ctx, func(w io.Writer) error {
			if _, err := w.Write([]byte("partial")); err != nil {
				return err
			}
			close(started)
			<-ctx.Done()
			return ctx.Err()
		})
		done <- err
	}()
	<-started
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)

	temps, err := filepath.Glob(filepath.Join(store.dir, ecdfUploadFile+"-*"))
	require.NoError(t, err)
	require.Empty(t, temps)
	manifestAfter, err := os.ReadFile(store.manifestPath())
	require.NoError(t, err)
	require.Equal(t, manifestBefore, manifestAfter)
	body, err := store.readCurrent(context.Background())
	require.NoError(t, err)
	require.Equal(t, "last-known-good", string(body))
}

func TestPersistentManifestLockFileIsReused(t *testing.T) {
	store := newJointECDFFileStore(t.TempDir(), 1, 2)
	require.NoError(t, os.MkdirAll(store.dir, 0755))
	first, err := store.acquireManifestLock(context.Background())
	require.NoError(t, err)
	require.NoError(t, store.releaseManifestLock(first))
	firstInfo, err := os.Stat(store.lockPath())
	require.NoError(t, err)

	second, err := store.acquireManifestLock(context.Background())
	require.NoError(t, err)
	secondInfo, err := os.Stat(store.lockPath())
	require.NoError(t, err)
	require.True(t, os.SameFile(firstInfo, secondInfo))
	require.NoError(t, store.releaseManifestLock(second))
}

func TestConcurrentPublicationsRemainSerialized(t *testing.T) {
	store := newJointECDFFileStore(t.TempDir(), 1, 2)
	store.lockWaitTimeout = 2 * time.Second
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	publish := func(first bool) error {
		_, err := store.publish(context.Background(), func(w io.Writer) error {
			current := active.Add(1)
			defer active.Add(-1)
			if current > maximum.Load() {
				maximum.Store(current)
			}
			if first {
				close(firstEntered)
				<-releaseFirst
			}
			_, err := w.Write([]byte("complete"))
			return err
		})
		return err
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- publish(true) }()
	<-firstEntered
	secondDone := make(chan error, 1)
	go func() { secondDone <- publish(false) }()
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int32(1), maximum.Load())
	close(releaseFirst)
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	require.Equal(t, int32(1), maximum.Load())
}

func countOpenDescriptorsForPath(t *testing.T, path string) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("descriptor inspection requires /proc")
	}
	require.NoError(t, err)
	count := 0
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err == nil && target == path {
			count++
		}
	}
	return count
}

func TestManifestLockRemainsOwnedUntilRelease(t *testing.T) {
	store := newJointECDFFileStore(t.TempDir(), 1, 2)
	require.NoError(t, os.MkdirAll(store.dir, 0755))

	owner, err := store.acquireManifestLock(context.Background())
	require.NoError(t, err)
	nextOwner := make(chan *jointECDFManifestLock, 1)
	acquireErr := make(chan error, 1)
	go func() {
		lock, err := store.acquireManifestLock(context.Background())
		nextOwner <- lock
		acquireErr <- err
	}()
	require.Never(t, func() bool {
		return len(acquireErr) > 0
	}, 50*time.Millisecond, 5*time.Millisecond)

	require.NoError(t, store.releaseManifestLock(owner))
	require.NoError(t, <-acquireErr)
	require.NoError(t, store.releaseManifestLock(<-nextOwner))
}

func TestReadCurrentRemainsAvailableDuringPublish(t *testing.T) {
	store := newJointECDFFileStore(t.TempDir(), 1, 2)
	_, err := store.publish(context.Background(), func(w io.Writer) error {
		_, err := w.Write([]byte("committed"))
		return err
	})
	require.NoError(t, err)

	buildStarted := make(chan struct{})
	finishBuild := make(chan struct{})
	publishDone := make(chan error, 1)
	go func() {
		_, err := store.publish(context.Background(), func(w io.Writer) error {
			close(buildStarted)
			<-finishBuild
			_, err := w.Write([]byte("replacement"))
			return err
		})
		publishDone <- err
	}()
	<-buildStarted

	body, err := store.readCurrent(context.Background())
	require.NoError(t, err)
	require.Equal(t, "committed", string(body))

	close(finishBuild)
	require.NoError(t, <-publishDone)
}

func TestReadCurrentFallsBackToPreviousWhilePublishHoldsLock(t *testing.T) {
	store := newJointECDFFileStore(t.TempDir(), 1, 2)
	for _, content := range []string{"previous", "current"} {
		_, err := store.publish(context.Background(), func(w io.Writer) error {
			_, err := w.Write([]byte(content))
			return err
		})
		require.NoError(t, err)
	}

	buildStarted := make(chan struct{})
	finishBuild := make(chan struct{})
	publishDone := make(chan error, 1)
	go func() {
		_, err := store.publish(context.Background(), func(w io.Writer) error {
			close(buildStarted)
			<-finishBuild
			_, err := w.Write([]byte("replacement"))
			return err
		})
		publishDone <- err
	}()
	<-buildStarted

	manifest, err := store.readManifest(store.manifestPath())
	require.NoError(t, err)
	require.NotNil(t, manifest.Current)
	require.NotNil(t, manifest.Previous)
	require.NoError(t, os.WriteFile(filepath.Join(store.dir, manifest.Current.File), []byte("corrupt"), 0644))

	body, err := store.readCurrent(context.Background())
	require.NoError(t, err)
	require.Equal(t, "previous", string(body))

	close(finishBuild)
	require.NoError(t, <-publishDone)
}
