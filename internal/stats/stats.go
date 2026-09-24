// Package stats provides small descriptive statistics helpers.
package stats

import (
	"cmp"
	"math"
	"slices"
)

// Summary describes a series of values.
type Summary struct {
	Mean float64 `json:"mean"`
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	P5   float64 `json:"p5"`
	P50  float64 `json:"p50"`
	P95  float64 `json:"p95"`
}

// Summarize returns the Summary of values, or the zero Summary when empty.
func Summarize(
	values []float64,
) Summary {
	if len(values) == 0 {
		return Summary{}
	}

	sorted := slices.Clone(values)
	slices.Sort(sorted)

	return Summary{
		Mean: Mean(values),
		Min:  sorted[0],
		Max:  sorted[len(sorted)-1],
		P5:   Percentile(sorted, 0.05),
		P50:  Percentile(sorted, 0.50),
		P95:  Percentile(sorted, 0.95),
	}
}

// Mean returns the arithmetic mean, or 0 when empty.
func Mean(
	values []float64,
) float64 {
	if len(values) == 0 {
		return 0
	}

	var sum float64
	for _, v := range values {
		sum += v
	}

	return sum / float64(len(values))
}

// Percentile returns the nearest-rank percentile p (0..1) of sorted values,
// or the zero value when empty. p outside 0..1 is clamped to the extremes.
func Percentile[T cmp.Ordered](
	sorted []T,
	p float64,
) T {
	if len(sorted) == 0 {
		var zero T

		return zero
	}

	rank := int(math.Ceil(p*float64(len(sorted)))) - 1

	return sorted[max(0, min(rank, len(sorted)-1))]
}

// StdDev returns the population standard deviation from running sums (Σx,
// Σx², n). Cancellation can make the variance slightly negative on constant
// series; it is clamped to 0.
func StdDev(
	sum, sumSq, n float64,
) float64 {
	if n == 0 {
		return 0
	}

	mean := sum / n

	return math.Sqrt(max(0, sumSq/n-mean*mean))
}
