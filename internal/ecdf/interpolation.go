package ecdf

import (
	"errors"
	"math"
)

type Polynomial []float64

func (cs Polynomial) Eval(x float64) float64 {
	if len(cs) == 0 {
		return 0
	}
	out := cs[0]
	for _, c := range cs[1:] {
		out = math.FMA(out, x, c)
	}
	return out
}

func linearPoly(x1, y1, x2, y2 float64) Polynomial {
	M := (y2 - y1) / (x2 - x1)
	B := y1 - M*x1
	return []float64{M, B}
}

func linearInterpolation(xs, ys []float64) (func(float64) float64, error) {
	// The xs define the boundaries of piecewise polynomials used to interpolate between the points.
	// They are laid out as follows:
	//
	// xs :=      xs[0]         xs[1]         xs[2]
	// fs := head   |   poly[0]   |   poly[1]   |   tail
	//
	n := len(xs)
	if n < 2 {
		// You need at least two points to interpolate
		return nil, errors.New("too few data points")
	}
	poly := make([]Polynomial, n-1)
	for i := range n - 1 {
		j := i + 1
		poly[i] = linearPoly(xs[i], ys[i], xs[j], ys[j])
	}

	// Extend the first polynomial so that the CDF is 0 at the first x
	m := poly[0][0]
	xs[0] -= ys[0] / m

	// Extend the last polynomial so that the CDF is 1 at the last x
	m = poly[n-2][0]
	xs[n-1] += (1 - ys[n-1]) / m

	// Return a function that finds the correct piecewise polynomial and evaluates it:
	return func(x float64) float64 {
		if x <= xs[0] {
			return 0
		}
		for i, xx := range xs[1:] {
			if x <= xx {
				return poly[i].Eval(x)
			}
		}
		return 1
	}, nil
}
