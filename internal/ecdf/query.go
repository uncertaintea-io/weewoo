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

// Query finds the CDF for the dependent variable in a Joint ECDF given the value of the independent variable.
func Query(ctx context.Context, jointECDF []byte, x float64) (CDF, error) {
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
		return nil, err
	}
	xs, ys, err := readPoints(&points)
	if err != nil {
		return nil, err
	}
	if len(xs) == 0 && len(ys) == 0 {
		return nil, nil
	}
	if !(slices.IsSorted(xs) && slices.IsSorted(ys)) {
		return nil, errors.New("data points not monotonically increasing")
	}
	if ys[0] < 0.0 || ys[len(ys)-1] > 1.0 {
		return nil, errors.New("cumulative probability not bounded to [0,1]")
	}
	return linearInterpolation(xs, ys)
}

// readPoints is a helper function that reads a series of coordinates
// for an empirical distribution from the jecdf tool.
// It returns two arrays:
//   - x values, which are samples of the dependent variable,
//   - y values, which are cumulative probabiltiies of each sample,
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
	ys := make([]float64, n)
	for i := range n {
		if err := binary.Read(reader, binary.BigEndian, &xs[i]); err != nil {
			return nil, nil, err
		}
		if err := binary.Read(reader, binary.BigEndian, &ys[i]); err != nil {
			return nil, nil, err
		}
	}
	return xs, ys, nil
}
