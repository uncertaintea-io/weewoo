// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

package collection

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRealClock(t *testing.T) {
	clock := newRealClock()
	now := clock.Now()
	//this should be true because the clock is the real clock
	assert.True(t, now.After(time.Now().Add(-time.Second)), "now should be after the current time minus one second")
	assert.True(t, now.Before(time.Now().Add(time.Second)), "now should be before the current time plus one second")
}

func TestRealTimer(t *testing.T) {
	timer := realTimer{Timer: time.NewTimer(time.Second)}
	assert.True(t, <-timer.C() != (time.Time{}), "timer should fire after one second")
	assert.True(t, timer.Stop(), "timer should be stopped")
}

func TestFakeClock(t *testing.T) {
	clock := NewFakeClock(time.Now())
	now := clock.Now()
	assert.True(t, now.After(time.Now().Add(-time.Second)), "now should be after the current time minus one second")
	assert.True(t, now.Before(time.Now().Add(time.Second)), "now should be before the current time plus one second")
}

func TestFakeTimer(t *testing.T) {
	clock := NewFakeClock(time.Now())
	timer := clock.NewTimer(time.Second)
	clock.Advance(time.Second)
	assert.True(t, <-timer.C() != (time.Time{}), "timer should fire after one second")
	assert.False(t, timer.Stop(), "timer should not be stopped")
}

func TestFakeTimerStopBeforeFire(t *testing.T) {
	clock := NewFakeClock(time.Now())
	timer := clock.NewTimer(time.Second)
	assert.True(t, timer.Stop(), "active timer should be stopped")
	clock.Advance(time.Second)
	select {
	case <-timer.C():
		t.Fatal("stopped timer should not fire")
	default:
	}
}
