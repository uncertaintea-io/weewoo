package ecdf

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os/exec"
	"time"
)

var (
	jecdf = flag.String("jecdf", "../../jecdf", "path to the jecdf tool")

	buildTimeout = 5 * time.Minute

	errJECDFExitedEarly = errors.New("jecdf exited before consuming all chunks")
)

func BuildJointECDF(store ChunkStore, serviceId int, indicatorId int, start, end time.Time) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()

	chunks := make(chan []byte, 2)

	type buildResult struct {
		out []byte
		err error
	}
	buildDone := make(chan buildResult, 1)
	scanDone := make(chan error, 1)

	go func() {
		defer close(chunks)
		scanDone <- store.ScanGoodChunks(ctx, serviceId, indicatorId, start, end, chunks)
	}()

	go func() {
		out, err := buildFromStream(ctx, chunks)
		buildDone <- buildResult{out: out, err: err}
	}()

	var out []byte
	for scanDone != nil || buildDone != nil {
		select {
		case err := <-scanDone:
			scanDone = nil
			if err != nil {
				cancel()
				if errors.Is(err, context.DeadlineExceeded) {
					return nil, fmt.Errorf("timeout building joint ECDF")
				}
				return nil, fmt.Errorf("failed to scan chunks: %w", err)
			}
			if buildDone == nil {
				return out, nil
			}
		case result := <-buildDone:
			buildDone = nil
			if result.err != nil {
				cancel()
				if errors.Is(result.err, context.DeadlineExceeded) {
					return nil, fmt.Errorf("timeout building joint ECDF")
				}
				return nil, fmt.Errorf("failed to build joint ECDF: %w", result.err)
			}
			out = result.out
			if scanDone == nil {
				return out, nil
			}
		case <-ctx.Done():
			cancel()
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("timeout building joint ECDF")
			}
			return nil, fmt.Errorf("failed to build joint ECDF: %w", ctx.Err())
		}
	}
	return nil, fmt.Errorf("failed to build joint ECDF: missing result")
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

	processDone := make(chan struct{})
	writeErr := make(chan error, 1)
	go func() {
		defer stdin.Close()
		for {
			select {
			case <-ctx.Done():
				writeErr <- ctx.Err()
				return
			case <-processDone:
				writeErr <- errJECDFExitedEarly
				return
			case chunk, ok := <-chunks:
				if !ok {
					writeErr <- nil
					return
				}
				for len(chunk) > 0 {
					n, err := stdin.Write(chunk)
					if err != nil {
						writeErr <- err
						return
					}
					chunk = chunk[n:]
				}
			}
		}
	}()

	waitErr := cmd.Wait()
	select {
	case err = <-writeErr:
	default:
		close(processDone)
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
