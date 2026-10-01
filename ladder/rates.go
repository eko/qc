package ladder

import (
	"cmp"
	"math"
	"slices"
)

// rateGapStep is the quality step (VMAF) at which two ladders are compared
// between their rungs.
const rateGapStep = 0.5

// rateGapEpsilon tells two gaps apart from the rounding of their
// interpolation.
const rateGapEpsilon = 1e-9

// RateGap is how the bitrates of a ladder compare with those of a reference
// ladder of the same title at equal quality: the bitrate of the ladder over
// the reference's, minus one (−0.3 is 30% less bitrate).
type RateGap struct {
	// VMAF is the highest quality both ladders reach, the lower of their
	// top rungs', and Top the gap there.
	VMAF float64 `json:"vmaf"`
	Top  float64 `json:"top"`
	// Low is the lowest quality both ladders reach. Mean is the gap
	// averaged from Low to VMAF (a geometric mean of the bitrate ratios),
	// Worst the largest gap in that range and WorstVMAF where it is.
	Low       float64 `json:"low"`
	Mean      float64 `json:"mean"`
	Worst     float64 `json:"worst"`
	WorstVMAF float64 `json:"worstVmaf"`
}

// CompareRates compares the bitrates of ladder with those of reference at
// equal quality, on the qualities both reach. Each ladder is read on its
// rungs, as verified (the measured bitrate and VMAF of the verification
// encodes) or as predicted when it was not, with the logarithm of the
// bitrate interpolated linearly in VMAF between two rungs: rungs are one
// quality step apart, where probes are several. Both ladders must come from
// the same digest of the same title for the comparison to mean anything. It
// returns false when a ladder has fewer than two rungs or the ladders share
// no quality.
func CompareRates(
	reference, ladder *Result,
) (RateGap, bool) {
	ref, other := ratePoints(reference), ratePoints(ladder)
	if len(ref) < 2 || len(other) < 2 {
		return RateGap{}, false
	}

	low := max(ref[0].vmaf, other[0].vmaf)
	top := min(ref[len(ref)-1].vmaf, other[len(other)-1].vmaf)

	if top <= low {
		return RateGap{}, false
	}

	gap := RateGap{VMAF: top, Low: low, Worst: math.Inf(-1)}
	sum, n := 0.0, 0

	// From the top down, so that the top quality is always compared.
	for v := top; v >= low; v -= rateGapStep {
		ratio := logRateAt(other, v) - logRateAt(ref, v)
		sum += ratio
		n++

		// Beyond rounding: equal gaps keep the highest quality.
		if ratio > gap.Worst+rateGapEpsilon {
			gap.Worst, gap.WorstVMAF = ratio, v
		}
	}

	gap.Top = math.Expm1(logRateAt(other, top) - logRateAt(ref, top))
	gap.Mean = math.Expm1(sum / float64(n))
	gap.Worst = math.Expm1(gap.Worst)

	return gap, true
}

// ratePoint is a rung as delivered: the logarithm of its bitrate and its
// quality.
type ratePoint struct {
	logRate, vmaf float64
}

// ratePoints returns the rungs of a ladder by increasing quality, as
// measured when they were verified. Rungs without a bitrate, and rungs that
// do not improve on a cheaper one, are left out: the curve read between the
// points must grow with the bitrate.
func ratePoints(
	res *Result,
) []ratePoint {
	if res == nil {
		return nil
	}

	points := make([]ratePoint, 0, len(res.Rungs))

	for _, r := range res.Rungs {
		bitrate, vmaf := r.Bitrate, r.PredictedVMAF
		if r.Measured != nil {
			bitrate, vmaf = r.Measured.Bitrate, r.Measured.VMAF
		}

		if bitrate > 0 {
			points = append(points, ratePoint{logRate: math.Log(float64(bitrate)), vmaf: vmaf})
		}
	}

	slices.SortFunc(points, func(a, b ratePoint) int { return cmp.Compare(a.logRate, b.logRate) })

	out := points[:0]

	for _, p := range points {
		if len(out) == 0 || p.vmaf > out[len(out)-1].vmaf {
			out = append(out, p)
		}
	}

	return out
}

// logRateAt reads the logarithm of the bitrate reaching vmaf on points,
// which must hold it between their first and last qualities.
func logRateAt(
	points []ratePoint,
	vmaf float64,
) float64 {
	i := max(1, slices.IndexFunc(points, func(p ratePoint) bool { return p.vmaf >= vmaf }))
	lo, hi := points[i-1], points[i]

	return lo.logRate + (vmaf-lo.vmaf)/(hi.vmaf-lo.vmaf)*(hi.logRate-lo.logRate)
}
