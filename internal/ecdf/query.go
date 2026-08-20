// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

package ecdf

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"slices"
	"strconv"
)

// CDF is a Cumulative Distribution Function, a native Go function
// that accepts all finite floating-point values, and returns the
// cumulative probability expressed as a float in the range [0,1].
type CDF func(float64) float64

// Query finds the dependent-variable CDF points in a Joint ECDF for the given
// independent-variable value. Callers choose how to interpolate these points
// when they need a continuous CDF.
func Query(ctx context.Context, jointECDF []byte, x float64) ([]float64, []float64, error) {
	var points bytes.Buffer
	err := runJECDF(
		ctx,
		[]string{"query", strconv.FormatFloat(x, 'g', -1, 64)},
		func(ctx context.Context, stdin io.Writer) error {
			if _, err := io.Copy(stdin, bytes.NewReader(jointECDF)); err != nil {
				return err
			}
			return nil
		},
		&points)
	if err != nil {
		return nil, nil, err
	}
	xs, ps, err := readPoints(&points)
	if err != nil {
		return nil, nil, err
	}
	if len(xs) != len(ps) {
		return nil, nil, errors.New("number of x and y values do not match")
	}
	if len(xs) == 0 && len(ps) == 0 {
		return nil, nil, nil
	}
	if !(slices.IsSorted(xs) && slices.IsSorted(ps)) {
		return nil, nil, errors.New("data points not monotonically increasing")
	}
	if ps[0] < 0.0 || ps[len(ps)-1] > 1.0 {
		return nil, nil, errors.New("cumulative probability not bounded to [0,1]")
	}
	return xs, ps, nil
}

// readPoints is a helper function that reads a series of coordinates
// for an empirical distribution from the jecdf tool.
// It returns two arrays:
//   - x values, which are samples of the dependent variable,
//   - p values, which are cumulative probabilities of each sample,
//     expressed as a float in the range [0, 1].
func readPoints(reader *bytes.Buffer) ([]float64, []float64, error) {
	// Read the number of points in the result:
	n, err := binary.ReadUvarint(reader)
	if err != nil {
		return nil, nil, err
	}
	if n > uint64(reader.Len()/16) {
		// Each point has two float64s, 8 bytes each. If the tool claims we need
		// more memory than what was actually read, it means something is wrong.
		// Catching this now is important for safely allocating memory buffers.
		return nil, nil, errors.New("truncated or corrupt response")
	}
	xs := make([]float64, n)
	ps := make([]float64, n)
	for i := range n {
		if err := binary.Read(reader, binary.BigEndian, &xs[i]); err != nil {
			return nil, nil, err
		}
		if err := binary.Read(reader, binary.BigEndian, &ps[i]); err != nil {
			return nil, nil, err
		}
	}
	return xs, ps, nil
}
