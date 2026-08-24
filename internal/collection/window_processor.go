// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

package collection

import (
	"context"
	"errors"
	"time"
)

var (
	errWindowHasNoMetrics = errors.New("collection window has no metrics")
	errWindowExpired      = errors.New("collection window expired")
)

type collectionWindow struct {
	Start time.Time
	End   time.Time
}

type windowOutcome string

const (
	windowCompleted     windowOutcome = "completed"
	windowMonitoringGap windowOutcome = "monitoring_gap"
	windowRetry         windowOutcome = "retry"
	windowDeferred      windowOutcome = "deferred"
	windowFailed        windowOutcome = "failed"
	windowCancelled     windowOutcome = "cancelled"
)

type windowAttempt struct {
	Window     collectionWindow
	Attempts   int
	ReadyAt    time.Time
	ExpiresAt  time.Time
	FailingFor time.Duration
}

type windowResult struct {
	Outcome    windowOutcome
	Err        error
	RetryAfter time.Duration
}

type windowPolicy interface {
	Classify(windowAttempt, error) windowResult
}

type windowProcessor struct {
	now func() time.Time
}

func newWindowProcessor(now func() time.Time) windowProcessor {
	if now == nil {
		now = time.Now
	}
	return windowProcessor{now: now}
}

func (p windowProcessor) Process(ctx context.Context, attempt windowAttempt, task func(context.Context) error, policy windowPolicy) windowResult {
	if err := ctx.Err(); err != nil {
		return windowResult{Outcome: windowCancelled, Err: err}
	}
	now := p.now()
	var err error
	if !attempt.ExpiresAt.IsZero() && !attempt.ExpiresAt.After(now) {
		err = errWindowExpired
	} else if !attempt.ReadyAt.IsZero() && attempt.ReadyAt.After(now) {
		return windowResult{Outcome: windowDeferred, RetryAfter: attempt.ReadyAt.Sub(now)}
	} else {
		err = task(ctx)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return windowResult{Outcome: windowCancelled, Err: ctxErr}
	}
	return policy.Classify(attempt, err)
}
