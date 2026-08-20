// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

package ecdf

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
)

type RenderResponse struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	XMin   float64 `json:"xMin"`
	XMax   float64 `json:"xMax"`
	YMin   float64 `json:"yMin"`
	YMax   float64 `json:"yMax"`
	// Masses are in image row order: X varies fastest and rows run from YMax to YMin.
	Masses []float64 `json:"masses"`
}

type RenderOptions int

const (
	RenderOptionLogX RenderOptions = 1 << iota
	RenderOptionLogY
)

const AllRenderOptions = RenderOptionLogX | RenderOptionLogY

func Render(ctx context.Context, jointECDF []byte, w, h int, options RenderOptions) (*RenderResponse, error) {
	if w < 2 || h < 2 {
		return nil, fmt.Errorf("render dimensions must be at least 2, were %d x %d", w, h)
	}
	if options < 0 || options&^AllRenderOptions != 0 {
		return nil, fmt.Errorf("unsupported render options: %d", options)
	}
	const valuesBeforeMasses = 4
	maxCells := math.MaxInt/8 - valuesBeforeMasses
	if w > maxCells/h {
		return nil, fmt.Errorf("render response size overflow: %d x %d", w, h)
	}

	var buf bytes.Buffer
	err := runJECDF(
		ctx,
		[]string{
			"render",
			strconv.FormatInt(int64(w), 10),
			strconv.FormatInt(int64(h), 10),
			strconv.FormatInt(int64(options), 10),
		},
		func(ctx context.Context, stdin io.Writer) error {
			if _, err := io.Copy(stdin, bytes.NewReader(jointECDF)); err != nil {
				return err
			}
			return nil
		},
		&buf)
	if err != nil {
		return nil, err
	}
	return readRenderResponse(&buf, w, h)
}

func readRenderResponse(reader *bytes.Buffer, width, height int) (*RenderResponse, error) {
	valueCount := 4 + width*height
	expectedBytes := valueCount * 8
	if reader.Len() != expectedBytes {
		return nil, fmt.Errorf("invalid jecdf render response size: got %d bytes, want %d", reader.Len(), expectedBytes)
	}

	values := make([]float64, valueCount)
	if err := binary.Read(reader, binary.BigEndian, values); err != nil {
		return nil, fmt.Errorf("read jecdf render response: %w", err)
	}
	if reader.Len() != 0 {
		return nil, errors.New("jecdf render response contains trailing data")
	}

	response := &RenderResponse{
		Width:  width,
		Height: height,
		XMin:   values[0],
		XMax:   values[1],
		YMin:   values[2],
		YMax:   values[3],
		Masses: values[4:],
	}
	if err := validateRenderResponse(response); err != nil {
		return nil, err
	}
	return response, nil
}

func validateRenderResponse(response *RenderResponse) error {
	bounds := []struct {
		name  string
		value float64
	}{
		{name: "xMin", value: response.XMin},
		{name: "xMax", value: response.XMax},
		{name: "yMin", value: response.YMin},
		{name: "yMax", value: response.YMax},
	}
	for _, bound := range bounds {
		if math.IsNaN(bound.value) || math.IsInf(bound.value, 0) {
			return fmt.Errorf("jecdf render response has non-finite %s", bound.name)
		}
	}
	if response.XMin > response.XMax {
		return errors.New("jecdf render response has xMin greater than xMax")
	}
	if response.YMin > response.YMax {
		return errors.New("jecdf render response has yMin greater than yMax")
	}
	for i, mass := range response.Masses {
		if math.IsNaN(mass) || math.IsInf(mass, 0) || mass < 0 || mass > 1 {
			return fmt.Errorf("jecdf render response has invalid mass at index %d: %g", i, mass)
		}
	}
	return nil
}
