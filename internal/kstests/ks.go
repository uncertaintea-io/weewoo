// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

package kstests

import (
	"cmp"
	"math"
	"slices"

	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

// Round to nearest integer. Rounds half integers to the nearest even integer.
func nint(x float64) int {
	var i int
	if x >= 0 {
		i = int(x + 0.5)
		if (i&1) != 0 && (x+0.5) == float64(i) {
			i -= 1
		}
	} else {
		i = int(x - 0.5)
		if (i&1) != 0 && (x-0.5) == float64(i) {
			i += 1
		}
	}
	return i
}

// kolmogorovPValue returns the asymptotic tail probability for the scaled KS
// statistic z = D*sqrt(n). Probabilities smaller than 1e-15 are returned as
// zero. The approximation is only valid for sufficiently large samples.
//
// Ported from CERN ROOT's TMath::KolmogorovProb implementation.
func kolmogorovPValue(z float64) float64 {
	var pValue float64
	if z < 0.2 {
		pValue = 1
	} else if z < 0.755 {
		const w = 2.50662827
		// c1 = -pi**2/8, c2 = 9*c1, c3 = 25*c1
		const c1 = -1.2337005501361697
		const c2 = -11.103304951225528
		const c3 = -30.842513753404244
		v := 1.0 / (z * z)
		lowerTailProbability := w * (math.Exp(c1*v) + math.Exp(c2*v) + math.Exp(c3*v)) / z
		// This complements the lower-tail CDF to calculate the p-value. It is
		// not an inversion of an already calculated p-value.
		pValue = 1 - lowerTailProbability
	} else if z < 6.8116 {
		fj := [4]float64{-2, -8, -18, -32}
		r := [4]float64{0, 0, 0, 0}
		v := z * z
		maxj := max(nint(3.0/z), 1)
		for j := 0; j < maxj; j += 1 {
			r[j] = math.Exp(fj[j] * v)
		}
		pValue = 2 * (r[0] - r[1] + r[2] - r[3])
	} else {
		pValue = 0
	}
	return pValue
}

// Result contains the two outputs of a one-sample Kolmogorov-Smirnov test.
type Result struct {
	// Statistic is the largest absolute difference between the sample's
	// empirical CDF and the reference CDF. It is commonly called D.
	Statistic float64

	// PValue is the probability, assuming the reference CDF is correct, of
	// observing a KS statistic at least as large as Statistic.
	PValue float64
}

// OneSample compares a sample with a reference CDF using a one-sample
// Kolmogorov-Smirnov test. The returned p-value is not the probability that the
// sample came from the reference distribution.
func OneSample(cdf func(float64) float64, samples []ecdf.Sample) Result {
	if len(samples) == 0 {
		return Result{
			Statistic: 0,
			PValue:    1,
		}
	}

	sum := samples[0].Count
	last := samples[0].Value
	sorted := true
	for _, sample := range samples[1:] {
		sorted = sorted && sample.Value >= last
		last = sample.Value
		sum += sample.Count
	}
	if !sorted {
		slices.SortFunc(samples, func(a, b ecdf.Sample) int {
			return cmp.Compare(a.Value, b.Value)
		})
	}

	n := float64(sum)
	maximumDifference := 0.0
	var seen uint64
	for _, sample := range samples {
		if sample.Count == 0 {
			continue
		}
		expectedProportion := cdf(sample.Value)
		seen += sample.Count - 1
		maximumDifference = math.Max(maximumDifference, math.Abs(expectedProportion-float64(seen)/n))
		seen++
		maximumDifference = math.Max(maximumDifference, math.Abs(float64(seen)/n-expectedProportion))
	}
	z := maximumDifference * math.Sqrt(float64(len(samples)))
	return Result{
		Statistic: maximumDifference,
		PValue:    kolmogorovPValue(z),
	}
}
