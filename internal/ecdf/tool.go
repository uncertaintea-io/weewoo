package ecdf

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
)

var (
	jecdf = flag.String("jecdf", "./jecdf", "path to the jecdf tool")

	errJECDFInputClosed = errors.New("jecdf exited before input completed")
)

type jecdfInputWriter func(context.Context, io.Writer) error

func runJECDF(ctx context.Context, command string, input jecdfInputWriter, output io.Writer, args ...string) error {
	cmdArgs := append([]string{command}, args...)
	cmd := exec.CommandContext(ctx, *jecdf, cmdArgs...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to open jecdf stdin: %w", err)
	}

	var stderr bytes.Buffer
	cmd.Stdout = output
	cmd.Stderr = &stderr

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
		if stderr.Len() > 0 {
			return fmt.Errorf("jecdf failed: %w: %s", waitErr, stderr.String())
		}
		return fmt.Errorf("jecdf failed: %w", waitErr)
	}
	return err
}
