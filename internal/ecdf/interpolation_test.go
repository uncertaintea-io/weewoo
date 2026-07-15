package ecdf

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLinearInterpolation(t *testing.T) {
	xs := []float64{1, 2}
	ys := []float64{0.1, 0.2}
	f := linearInterpolation(xs, ys)
	for _, tc := range []struct{ x, y float64 }{
		{-1, 0},
		{0, 0},
		{0.625, 0.0625},
		{1, 0.1},
		{1.25, 0.125},
		{2,0.2},
		{5, 0.5},
		{7.5, 0.75},
		{10, 1},
		{12, 1},
	} {
		got := f(tc.x)
		assert.Equal(t, tc.y, got, "f(%f): want %f, got %f", tc.x, tc.y, got)
	}
}
