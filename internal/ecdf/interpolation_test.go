// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

package ecdf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLinearInterpolation(t *testing.T) {
	xs := []float64{1, 2, 10}
	ys := []float64{0.1, 0.2, 1}
	f, err := LinearInterpolation(xs, ys)
	require.NoError(t, err)

	for _, tc := range []struct{ x, y float64 }{
		{-1, 0},
		{0, 0},
		{0.5, 0},
		{1, 0.1},
		{1.25, 0.125},
		{2, 0.2},
		{5, 0.5},
		{7.5, 0.75},
		{10, 1},
		{12, 1},
	} {
		got := f(tc.x)
		assert.Equal(t, tc.y, got, "f(%f): want %f, got %f", tc.x, tc.y, got)
	}
}

func TestLinearTooFew(t *testing.T) {
	xs := []float64{1}
	ys := []float64{1}
	f, err := LinearInterpolation(xs, ys)
	assert.Nil(t, f)
	require.Error(t, err)
}
