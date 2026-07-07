package ecdf

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"time"

	"golang.org/x/sync/errgroup"
)

var (
	jecdf = flag.String("jecdf", "./jecdf", "path to the jecdf tool")

	buildTimeout = 5 * time.Minute

	errJECDFExitedEarly = errors.New("jecdf exited before consuming all chunks")
)

// BuildJointECDF builds a joint ECDF from good chunks in the time range.
func BuildJointECDF(store ChunkStore, serviceId, indicatorId int, start, end time.Time) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()

	return BuildJointECDFContext(ctx, store, serviceId, indicatorId, start, end)
}

// BuildJointECDFContext builds a joint ECDF using the supplied context for scanning and subprocess execution.
func BuildJointECDFContext(ctx context.Context, store ChunkStore, serviceId, indicatorId int, start, end time.Time) ([]byte, error) {
	chunks := make(chan []byte, 2)

	group, ctx := errgroup.WithContext(ctx)

	group.Go(func() error {
		defer close(chunks)
		err := store.ScanGoodChunks(ctx, serviceId, indicatorId, start, end, chunks)
		return buildError("failed to scan chunks", err)
	})

	var out []byte
	group.Go(func() error {
		var err error
		out, err = buildFromStream(ctx, chunks)
		return buildError("failed to build joint ECDF", err)
	})

	if err := group.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

func buildError(prefix string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("timeout building joint ECDF")
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func buildFromStream(ctx context.Context, chunks <-chan []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, *jecdf, "build")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to open jecdf stdin: %w", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start jecdf: %w", err)
	}

	processExited := make(chan struct{})
	writeErr := make(chan error, 1)
	go func() {
		writeErr <- writeChunks(ctx, stdin, chunks, processExited)
	}()

	waitErr := cmd.Wait()
	select {
	case err = <-writeErr:
	default:
		close(processExited)
		_ = stdin.Close()
		err = <-writeErr
	}

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if waitErr != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("jecdf failed: %w: %s", waitErr, stderr.String())
		}
		return nil, fmt.Errorf("jecdf failed: %w", waitErr)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to write chunks to jecdf: %w", err)
	}
	return stdout.Bytes(), nil
}

func writeChunks(ctx context.Context, stdin io.WriteCloser, chunks <-chan []byte, processExited <-chan struct{}) error {
	defer stdin.Close()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-processExited:
			return errJECDFExitedEarly
		case chunk, ok := <-chunks:
			if !ok {
				return nil
			}
			if _, err := io.Copy(stdin, bytes.NewReader(chunk)); err != nil {
				return err
			}
		}
	}
}
