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
	cdf, err := Query(ctx, sampleJointECDF, value)
	require.NoError(t, err)
	assert.NotNil(t, cdf)
	assert.Equal(t, 0.0, cdf(-1000))
	assert.Equal(t, 1.0, cdf(1000))
	assert.Greater(t, cdf(3), 0.0)
	assert.Less(t, cdf(3), 1.0)
}
