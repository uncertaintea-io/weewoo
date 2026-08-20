// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

package collection

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/uncertaintea-io/weewoo/internal/config"
)

func TestRecoveryDelayEntersHourlyProbeMode(t *testing.T) {
	cfg := config.NewFakeConfig()

	assert.Equal(t, time.Hour, recoveryDelay(cfg, 12, time.Hour))
}

func TestRecoveryDelayUsesExponentialBackoffBeforeProbeMode(t *testing.T) {
	cfg := config.NewFakeConfig()

	assert.Equal(t, time.Second, recoveryDelay(cfg, 1, time.Minute))
	assert.Equal(t, 2*time.Second, recoveryDelay(cfg, 2, time.Minute))
	assert.Equal(t, time.Minute, recoveryDelay(cfg, 10, time.Minute))
}
