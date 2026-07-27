package ecdf

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadRenderResponse(t *testing.T) {
	payload := renderPayload(t, []float64{
		10, 20, 100, 200,
		0.1, 0.2,
		0.3, 0.4,
	})

	response, err := readRenderResponse(bytes.NewBuffer(payload), 2, 2)

	require.NoError(t, err)
	assert.Equal(t, &RenderResponse{
		Width:  2,
		Height: 2,
		XMin:   10,
		XMax:   20,
		YMin:   100,
		YMax:   200,
		Masses: []float64{0.1, 0.2, 0.3, 0.4},
	}, response)
}

func TestReadRenderResponseRejectsUnexpectedSize(t *testing.T) {
	_, err := readRenderResponse(bytes.NewBuffer(renderPayload(t, []float64{0, 1, 0, 1})), 2, 2)

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
			_, err := readRenderResponse(bytes.NewBuffer(renderPayload(t, tt.values)), 2, 2)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.error)
		})
	}
}

func TestRenderRejectsInvalidDimensionsBeforeRunningTool(t *testing.T) {
	setJECDFTool(t, "/does/not/exist")

	response, err := Render(context.Background(), nil, 1, 2)

	require.Error(t, err)
	assert.Nil(t, response)
	assert.Contains(t, err.Error(), "dimensions must be at least 2")
}

func TestRenderCapturesToolOutput(t *testing.T) {
	payload := renderPayload(t, []float64{
		10, 20, 100, 200,
		0.1, 0.2, 0.3,
		0.4, 0.5, 0.6,
	})
	outputPath := filepath.Join(t.TempDir(), "render.bin")
	require.NoError(t, os.WriteFile(outputPath, payload, 0600))
	setJECDFTool(t, writeFakeJECDF(t, fmt.Sprintf(`if [ "$1" != "render" ] || [ "$2" != "3" ] || [ "$3" != "2" ]; then
	exit 2
fi
cat >/dev/null
cat %q
`, outputPath)))

	response, err := Render(context.Background(), []byte("jecdf"), 3, 2)

	require.NoError(t, err)
	assert.Equal(t, &RenderResponse{
		Width:  3,
		Height: 2,
		XMin:   10,
		XMax:   20,
		YMin:   100,
		YMax:   200,
		Masses: []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6},
	}, response)
}

func renderPayload(t *testing.T, values []float64) []byte {
	t.Helper()
	var payload bytes.Buffer
	require.NoError(t, binary.Write(&payload, binary.BigEndian, values))
	return payload.Bytes()
}
