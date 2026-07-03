package ecdf

import (
	"bytes"
	"flag"
	"fmt"
	"os/exec"
	"time"
)

var (
	jecdf = flag.String("jecdf", "../../jecdf", "path to the jecdf tool")

	buildTimeout = 5 * time.Minute
)

func BuildECDF(store ChunkStore, serviceId int, indicatorId int, start, end time.Time) ([]byte, error) {
	// TODO: Read chunks form store and feed to tool in parallel.
	chunks := make(chan []byte, 2)
	var result []byte

	done := make(chan error)
	go func() {
		var err error
		result, err = buildFromStream(chunks)
		done <- err
	}()

	err := store.ScanGoodChunks(serviceId, indicatorId, start, end, chunks)
	close(chunks)
	if err != nil {
		return nil, fmt.Errorf("failed to scan chunks: %w", err)
	}

	select {
	case err = <-done:
		if err != nil {
			return nil, fmt.Errorf("failed to build ECDF: %w", err)
		}
		return result, nil
	case <-time.After(buildTimeout):
		return nil, fmt.Errorf("timeout building ECDF")
	}
}

func buildFromStream(chunks <-chan []byte) ([]byte, error) {
	cmd := exec.Command(*jecdf, "build")
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

	writeErr := make(chan error, 1)
	go func() {
		defer stdin.Close()
		for chunk := range chunks {
			for len(chunk) > 0 {
				n, err := stdin.Write(chunk)
				if err != nil {
					writeErr <- err
					return
				}
				chunk = chunk[n:]
			}
		}
		writeErr <- nil
	}()

	waitErr := cmd.Wait()
	if err := <-writeErr; err != nil {
		return nil, fmt.Errorf("failed to write chunks to jecdf: %w", err)
	}
	if waitErr != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("jecdf failed: %w: %s", waitErr, stderr.String())
		}
		return nil, fmt.Errorf("jecdf failed: %w", waitErr)
	}
	return stdout.Bytes(), nil
}
