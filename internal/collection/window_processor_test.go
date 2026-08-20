// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

package collection

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/uncertaintea-io/weewoo/internal/config"
)

func TestWindowProcessorUsesImportPolicyToClassifyMissingMetricsAsMonitoringGap(t *testing.T) {
	processor := newWindowProcessor(func() time.Time {
		return time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	})
	window := collectionWindow{
		Start: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC),
	}

	result := processor.Process(context.Background(), windowAttempt{Window: window}, func(context.Context) error {
		return errWindowHasNoMetrics
	}, historicalImportWindowPolicy{})

	require.Equal(t, windowMonitoringGap, result.Outcome)
	require.ErrorIs(t, result.Err, errWindowHasNoMetrics)
}

func TestWindowProcessorUsesRecoveryPolicyToExpireWithoutCollecting(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	processor := newWindowProcessor(func() time.Time { return now })
	collected := false

	result := processor.Process(context.Background(), windowAttempt{
		Window:    collectionWindow{Start: now.Add(-2 * time.Hour), End: now.Add(-time.Hour)},
		Attempts:  4,
		ExpiresAt: now.Add(-time.Second),
	}, func(context.Context) error {
		collected = true
		return nil
	}, recoveryWindowPolicy{cfg: config.NewFakeConfig()})

	require.Equal(t, windowMonitoringGap, result.Outcome)
	require.ErrorIs(t, result.Err, errWindowExpired)
	require.False(t, collected)
}

func TestWindowProcessorDefersRecoveryUntilReady(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	processor := newWindowProcessor(func() time.Time { return now })
	collected := false

	result := processor.Process(context.Background(), windowAttempt{
		Window:  collectionWindow{Start: now.Add(-time.Hour), End: now},
		ReadyAt: now.Add(45 * time.Second),
	}, func(context.Context) error {
		collected = true
		return nil
	}, recoveryWindowPolicy{cfg: config.NewFakeConfig()})

	require.Equal(t, windowDeferred, result.Outcome)
	require.Equal(t, 45*time.Second, result.RetryAfter)
	require.False(t, collected)
}

func TestWindowProcessorUsesRecoveryPolicyToScheduleRetry(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	processor := newWindowProcessor(func() time.Time { return now })
	collectionErr := errors.New("prometheus unavailable")

	result := processor.Process(context.Background(), windowAttempt{
		Window:     collectionWindow{Start: now.Add(-time.Minute), End: now},
		Attempts:   3,
		FailingFor: time.Minute,
	}, func(context.Context) error {
		return collectionErr
	}, recoveryWindowPolicy{cfg: config.NewFakeConfig()})

	require.Equal(t, windowRetry, result.Outcome)
	require.ErrorIs(t, result.Err, collectionErr)
	require.Equal(t, 4*time.Second, result.RetryAfter)
}
