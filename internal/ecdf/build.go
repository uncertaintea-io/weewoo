package ecdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"golang.org/x/sync/errgroup"
)

// BuildJointECDF builds a joint ECDF from the good chunks in a generation.
func BuildJointECDF(ctx context.Context, store ChunkStore, serviceID, indicatorID int, generation int64, writer io.Writer) error {
	chunks := make(chan []byte, 2)

	group, ctx := errgroup.WithContext(ctx)

	group.Go(func() error {
		defer close(chunks)
		err := store.ScanGoodChunks(ctx, serviceID, indicatorID, generation, chunks)
		return buildError("failed to scan chunks", err)
	})

	group.Go(func() error {
		return buildFromStream(ctx, chunks, writer)
	})

	return group.Wait()
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

func buildFromStream(ctx context.Context, chunks <-chan []byte, writer io.Writer) error {
	return runJECDF(
		ctx,
		[]string{"build", "-ulp", "5"},
		func(ctx context.Context, stdin io.Writer) error {
			if err := writeChunks(ctx, stdin, chunks); err != nil {
				return fmt.Errorf("failed to write chunks to jecdf: %w", err)
			}
			return nil
		},
		writer)
}

func writeChunks(ctx context.Context, stdin io.Writer, chunks <-chan []byte) error {
	for {
		select {
		case <-ctx.Done():
			if errors.Is(context.Cause(ctx), errJECDFInputClosed) {
				return errors.New("jecdf exited before consuming all chunks")
			}
			return ctx.Err()
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
