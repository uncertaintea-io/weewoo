package alerting

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

func TestSeverityForCount(t *testing.T) {
	assert.Equal(t, SeverityWarning, severityForCount(1, 3))
	assert.Equal(t, SeverityWarning, severityForCount(2, 3))
	assert.Equal(t, SeverityCritical, severityForCount(3, 3))
	assert.Equal(t, SeverityCritical, severityForCount(4, 3))
}

func TestSanitizeDetailsRedactsCredentials(t *testing.T) {
	details := "GET https://alice:hunter2@prometheus.example/api?token=abc Authorization: Bearer secret"

	sanitized := sanitizeDetails(details)

	assert.NotContains(t, sanitized, "hunter2")
	assert.NotContains(t, sanitized, "token=abc")
	assert.NotContains(t, sanitized, "Bearer secret")
	assert.Contains(t, sanitized, "token=[redacted]")
}

func TestCollectionDescriptionExplainsRetry(t *testing.T) {
	retryAt := time.Date(2026, 7, 24, 12, 30, 0, 0, time.UTC)
	description := collectionDescription("checkout", 3, retryAt)

	assert.Contains(t, description, "3 consecutive")
	assert.Contains(t, description, retryAt.Format(time.RFC3339))
}

func TestSanitizeErrorAcceptsNil(t *testing.T) {
	assert.Empty(t, sanitizeError(nil))
	assert.Equal(t, "safe", sanitizeError(errors.New("safe")))
}

func TestWeightedInputAcrossChunks(t *testing.T) {
	input, err := weightedAverage([][]ecdf.Sample{
		{{Value: 1, Count: 2}, {Value: 4, Count: 1}},
		{{Value: 7, Count: 3}},
	})

	require.NoError(t, err)
	assert.Equal(t, 4.5, input)
}

func TestAggregateSamplesAcrossChunks(t *testing.T) {
	samples, err := aggregateSamples([][]ecdf.Sample{
		{{Value: 2, Count: 3}, {Value: 1, Count: 2}},
		{{Value: 2, Count: 4}, {Value: 3, Count: 1}},
	})

	require.NoError(t, err)
	assert.Equal(t, []ecdf.Sample{
		{Value: 1, Count: 2},
		{Value: 2, Count: 7},
		{Value: 3, Count: 1},
	}, samples)
}
