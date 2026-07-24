package alerting

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
