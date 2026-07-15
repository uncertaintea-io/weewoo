package ecdf

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"strconv"
)

// Query finds the ECDF for an independent variable in a Joint ECDF given the value of the dependent variable.
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
	return readPoints(&points)
}

func readPoints(reader *bytes.Buffer) ([]float64, []float64, error) {
	// Read the number of points in the result:
	n, err := binary.ReadUvarint(reader)
	if err != nil {
		return nil, nil, err
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
