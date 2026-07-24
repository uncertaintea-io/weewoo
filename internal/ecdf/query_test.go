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
	if cdf == nil {
		t.Log("jecdf returned zero points; no CDF is available yet")
		return
	}
	assert.Equal(t, 0.0, cdf(-1000))
	assert.Equal(t, 1.0, cdf(1000))
	assert.Greater(t, cdf(3), 0.0)
	assert.Less(t, cdf(3), 1.0)
}

func TestQueryAcceptsZeroPointResult(t *testing.T) {
	setJECDFTool(t, writeFakeJECDF(t, `if [ "$1" != "query" ]; then
	exit 2
fi
cat >/dev/null
printf '\000'
`))

	cdf, err := Query(context.Background(), nil, 0)

	require.NoError(t, err)
	assert.Nil(t, cdf)
}
