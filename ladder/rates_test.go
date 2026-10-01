package ladder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rungsOf builds verified rungs from (bitrate in kb/s, VMAF) pairs, top
// first as a ladder lists them.
func rungsOf(
	pairs ...float64,
) []Rung {
	var out []Rung

	for i := 0; i < len(pairs); i += 2 {
		out = append(out, Rung{
			// The plan differs from the verification, which is what counts.
			Bitrate: int64(pairs[i] * 1100), PredictedVMAF: pairs[i+1] - 1,
			Measured: &Measurement{Bitrate: int64(pairs[i] * 1000), VMAF: pairs[i+1]},
		})
	}

	return out
}

func TestCompareRates(
	t *testing.T,
) {
	reference := &Result{Rungs: rungsOf(8000, 93, 4000, 87, 2000, 81, 1000, 75)}

	planned := &Result{Rungs: []Rung{
		{Bitrate: 4_000_000, PredictedVMAF: 93}, {Bitrate: 2_000_000, PredictedVMAF: 87},
		{Bitrate: 1_000_000, PredictedVMAF: 81}, {Bitrate: 500_000, PredictedVMAF: 75},
	}}

	testCases := []struct {
		name   string
		ladder *Result
		want   RateGap
	}{
		{
			name:   "half the bitrate at every quality",
			ladder: &Result{Rungs: rungsOf(4000, 93, 2000, 87, 1000, 81, 500, 75)},
			want:   RateGap{VMAF: 93, Top: -0.5, Low: 75, Mean: -0.5, Worst: -0.5, WorstVMAF: 93},
		},
		{
			name:   "an unverified ladder is read as planned",
			ladder: planned,
			want:   RateGap{VMAF: 93, Top: -0.5, Low: 75, Mean: -0.5, Worst: -0.5, WorstVMAF: 93},
		},
		{
			name: "costlier at the top, cheaper at the bottom",
			// 10% more at 93, the same at 87, 20% less at 81 and 75.
			ladder: &Result{Rungs: rungsOf(8800, 93, 4000, 87, 1600, 81, 800, 75)},
			want:   RateGap{VMAF: 93, Top: 0.10, Low: 75, Mean: -0.0895, Worst: 0.10, WorstVMAF: 93},
		},
		{
			name: "compared where both reach: the lower top, the higher bottom",
			// Its top rung is above the reference's: at 93, between its rungs
			// at 96 and 90, it needs √(8000·4000) kb/s, 29% less.
			ladder: &Result{Rungs: rungsOf(8000, 96, 4000, 90, 2000, 84, 1000, 78)},
			want:   RateGap{VMAF: 93, Top: -0.2929, Low: 78, Mean: -0.2929, Worst: -0.2929, WorstVMAF: 93},
		},
		{
			name: "a rung no better than a cheaper one is left out",
			// The 3000 kb/s rung scores below the 2000 kb/s one.
			ladder: &Result{Rungs: rungsOf(4000, 93, 3000, 86, 2000, 87, 1000, 81, 500, 75)},
			want:   RateGap{VMAF: 93, Top: -0.5, Low: 75, Mean: -0.5, Worst: -0.5, WorstVMAF: 93},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := CompareRates(reference, testCase.ladder)
			require.True(t, ok)

			assert.InDelta(t, testCase.want.VMAF, got.VMAF, 1e-9)
			assert.InDelta(t, testCase.want.Low, got.Low, 1e-9)
			assert.InDelta(t, testCase.want.Top, got.Top, 1e-3)
			assert.InDelta(t, testCase.want.Mean, got.Mean, 1e-3)
			assert.InDelta(t, testCase.want.Worst, got.Worst, 1e-3)
			assert.InDelta(t, testCase.want.WorstVMAF, got.WorstVMAF, 1e-9)
		})
	}
}

func TestCompareRatesNothingToCompare(
	t *testing.T,
) {
	reference := &Result{Rungs: rungsOf(8000, 93, 4000, 87)}

	testCases := []struct {
		name   string
		ref    *Result
		ladder *Result
	}{
		{name: "no ladder", ref: reference},
		{name: "no reference", ladder: reference},
		{name: "a single rung", ref: reference, ladder: &Result{Rungs: rungsOf(8000, 93)}},
		{name: "rungs without a bitrate", ref: reference, ladder: &Result{Rungs: []Rung{{PredictedVMAF: 93}, {PredictedVMAF: 87}}}},
		{name: "no quality in common", ref: reference, ladder: &Result{Rungs: rungsOf(500, 60, 250, 50)}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, ok := CompareRates(testCase.ref, testCase.ladder)
			assert.False(t, ok)
		})
	}
}
