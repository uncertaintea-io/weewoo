package ecdf

import (
	"context"
	_ "embed"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/ecdf.bin
var sampleJointECDF []byte

func TestQueryWithRealTool(t *testing.T) {
	setJECDFTool(t, "../../jecdf")
	if !jecdfExists(t) {
		t.Skip("jecdf tool not found or not executable, skipping test")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const value = 1.5
	xs, ys, err := Query(ctx, sampleJointECDF, value)
	require.NoError(t, err)
	assert.NotEmpty(t, xs)
	assert.NotEmpty(t, ys)
}
