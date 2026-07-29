package ecdf

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderJointECDFWithRealTool(t *testing.T) {
	setJECDFTool(t, "../../jecdf")
	if !jecdfExists(t) {
		t.Skip("jecdf tool not found or not executable, skipping test")
	}

	const serviceID, indicatorID = 1, 1
	timestamp := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	chunk, err := Encode(
		timestamp,
		[]Sample{{Value: 10, Count: 1}, {Value: 20, Count: 1}},
		[]Sample{{Value: 100, Count: 1}, {Value: 200, Count: 1}},
	)
	require.NoError(t, err)
	store := NewFakeChunkStore()
	require.NoError(t, store.WriteChunk(serviceID, indicatorID, timestamp, chunk))

	var jointECDF bytes.Buffer
	require.NoError(t, BuildJointECDFContext(context.Background(), store, serviceID, indicatorID, &jointECDF))

	response, err := Render(context.Background(), jointECDF.Bytes(), 2, 2, RenderOptionLogY)

	require.NoError(t, err)
	assert.Equal(t, 2, response.Width)
	assert.Equal(t, 2, response.Height)
	assert.Equal(t, 10.0, response.XMin)
	assert.Equal(t, 20.0, response.XMax)
	assert.Equal(t, 100.0, response.YMin)
	assert.Equal(t, 200.0, response.YMax)
	assert.InDeltaSlice(t, []float64{
		0.02144660940672627, 0.125,
		0.10355339059327373, 0,
	}, response.Masses, 1e-12)
}

func TestReadRenderResponseRejectsUnexpectedSize(t *testing.T) {
	_, err := readRenderResponse(bytes.NewBuffer(invalidRenderPayload(t, []float64{0, 1, 0, 1})), 2, 2)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "got 32 bytes, want 64")
}

func TestReadRenderResponseRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		error  string
	}{
		{name: "reversed bounds", values: []float64{2, 1, 0, 1, 0, 0, 0, 0}, error: "xMin greater than xMax"},
		{name: "negative mass", values: []float64{0, 1, 0, 1, 0, -0.1, 0, 0}, error: "invalid mass at index 1"},
		{name: "oversized mass", values: []float64{0, 1, 0, 1, 0, 0, 1.1, 0}, error: "invalid mass at index 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := readRenderResponse(bytes.NewBuffer(invalidRenderPayload(t, tt.values)), 2, 2)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.error)
		})
	}
}

func TestRenderRejectsInvalidDimensionsBeforeRunningTool(t *testing.T) {
	setJECDFTool(t, "/does/not/exist")

	response, err := Render(context.Background(), nil, 1, 2, 0)

	require.Error(t, err)
	assert.Nil(t, response)
	assert.Contains(t, err.Error(), "dimensions must be at least 2")
}

func TestRenderRejectsUnsupportedOptionsBeforeRunningTool(t *testing.T) {
	setJECDFTool(t, "/does/not/exist")

	response, err := Render(context.Background(), nil, 2, 2, 4)

	require.Error(t, err)
	assert.Nil(t, response)
	assert.Contains(t, err.Error(), "unsupported render options")
}

func invalidRenderPayload(t *testing.T, values []float64) []byte {
	t.Helper()
	var payload bytes.Buffer
	require.NoError(t, binary.Write(&payload, binary.BigEndian, values))
	return payload.Bytes()
}
