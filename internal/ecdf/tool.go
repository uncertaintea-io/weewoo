package ecdf

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
)

var (
	jecdf = flag.String("jecdf", "./jecdf", "path to the jecdf tool")

	errJECDFInputClosed = errors.New("jecdf exited before input completed")
)

type jecdfInputWriter func(context.Context, io.Writer) error

// runJECDF runs the jecdf tool with the given args, handling the details of I/O and failure.
// It uses the provided input function to stream input to stdin, and copies stdout to output.
func runJECDF(ctx context.Context, args []string, input jecdfInputWriter, output io.Writer) error {
	cmd := exec.CommandContext(ctx, *jecdf, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to open jecdf stdin: %w", err)
	}

	cmd.Stdout = output
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start jecdf: %w", err)
	}

	inputCtx, cancelInput := context.WithCancelCause(ctx)
	defer cancelInput(nil)

	writeErr := make(chan error, 1)
	go func() {
		writeErr <- input(inputCtx, stdin)
		_ = stdin.Close()
	}()

	waitErr := cmd.Wait()
	select {
	case err = <-writeErr:
	default:
		cancelInput(errJECDFInputClosed)
		_ = stdin.Close()
		err = <-writeErr
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}
	if waitErr != nil {
		return fmt.Errorf("jecdf failed: %w", waitErr)
	}
	return err
}
