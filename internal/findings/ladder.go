package findings

import (
	"math"

	"github.com/eko/qc/ladder"
	"github.com/eko/qc/quality"
)

// Codes of a ladder. Index is the rung concerned, 0 (the top rung) for the
// ladder-wide codes.
const (
	// NoRungs: no rung could be selected; no other finding follows.
	NoRungs Code = "no-rungs"
	// TopVMAFMissed: the top rung stays below the targeted quality, the
	// title's best probed quality. Limit is the target.
	TopVMAFMissed Code = "top-vmaf-missed"
	// TopVMAFReached: the top rung reaches the targeted quality. Limit is
	// the target.
	TopVMAFReached Code = "top-vmaf-reached"
	// LighterThanApple: an H.264 top rung lighter than Apple's static one.
	// Value is the bitrate saved (a share), Limit AppleTopH264.
	LighterThanApple Code = "lighter-than-apple"
	// Verification: how far the measured VMAF of the verification encodes
	// strays from the prediction, at worst. Value is that gap, Limit half a
	// rung step: beyond it, a warning, as neighbouring rungs may overlap.
	Verification Code = "verification"
	// Extrapolated: the rung targets a quality outside the probed range of
	// its resolution, so its prediction is extrapolated.
	Extrapolated Code = "extrapolated"
	// GrainMismatch: the rung's synthesised film grain strays from the
	// source's (ladder.Rung.Grain).
	GrainMismatch Code = "grain-mismatch"
	// Calibrated: the rung missed its prediction by more than Limit
	// (ladder.CalibrationTolerance) and had its CRF corrected.
	Calibrated Code = "calibrated"
	// BandedRung: the rung shows visible banding on a share of its scored
	// frames (Value) with CAMBI above Limit.
	BandedRung Code = "banded-rung"
	// RankConflict: VMAF ranks rung Index above rung Other, XPSNR below
	// (ladder.RankConflicts).
	RankConflict Code = "rank-conflict"
)

const (
	// AppleTopH264 (bits/s) is the top rung of Apple's HLS authoring spec
	// H.264 ladder, the static reference a per-title ladder is measured
	// against.
	AppleTopH264 = 7_800_000
	// topVMAFTolerance absorbs curve interpolation noise when checking
	// whether the top rung reached the target quality.
	topVMAFTolerance = 0.5
	// codecH264 is the only codec Apple's static ladder is given for.
	codecH264 = "h264"
)

// Ladder lists the findings of a ladder: the ladder-wide ones, then the
// rungs' one by one, then the banding and rank checks of the verified
// rungs.
func Ladder(
	r *ladder.Result,
) []Finding {
	if len(r.Rungs) == 0 {
		return []Finding{{Level: Warn, Code: NoRungs}}
	}

	top, c := r.Rungs[0], r.Constraints
	out := []Finding{topFinding(top, c.TopVMAF)}

	if r.Codec.Name == codecH264 && top.Bitrate < AppleTopH264 {
		out = append(out, Finding{Level: OK, Code: LighterThanApple, Value: 1 - float64(top.Bitrate)/AppleTopH264, Limit: AppleTopH264})
	}

	if f, ok := verificationFinding(r.Rungs, c.Step); ok {
		out = append(out, f)
	}

	for i, rung := range r.Rungs {
		out = append(out, rungFindings(i, rung)...)
	}

	for _, i := range ladder.BandedRungs(r.Rungs) {
		out = append(out, Finding{
			Level: Warn, Code: BandedRung, Index: i,
			Value: r.Rungs[i].Measured.BandedShare(), Limit: quality.BandingThreshold,
		})
	}

	for _, conflict := range ladder.RankConflicts(r.Rungs) {
		out = append(out, Finding{Level: Warn, Code: RankConflict, Index: conflict.Higher, Other: conflict.Lower})
	}

	return append(out, hdrLadderFindings(r)...)
}

// topFinding says whether the top rung reaches the targeted quality.
func topFinding(
	top ladder.Rung,
	target float64,
) Finding {
	if top.PredictedVMAF < target-topVMAFTolerance {
		return Finding{Level: Warn, Code: TopVMAFMissed, Limit: target}
	}

	return Finding{Level: OK, Code: TopVMAFReached, Limit: target}
}

// verificationFinding reports the worst gap between measured and predicted
// VMAF, when rungs were verified: within half a rung step, rungs stay
// distinct.
func verificationFinding(
	rungs []ladder.Rung,
	step float64,
) (Finding, bool) {
	worst, verified := 0.0, false

	for _, r := range rungs {
		if r.Measured != nil {
			worst, verified = max(worst, math.Abs(r.Measured.VMAF-r.PredictedVMAF)), true
		}
	}

	f := Finding{Level: OK, Code: Verification, Value: worst, Limit: step / 2}
	if worst > f.Limit {
		f.Level = Warn
	}

	return f, verified
}

// rungFindings are the findings of rung i on its own.
func rungFindings(
	i int,
	rung ladder.Rung,
) []Finding {
	var out []Finding

	if rung.Extrapolated {
		out = append(out, Finding{Level: Warn, Code: Extrapolated, Index: i})
	}

	if g := rung.Grain; g != nil && !g.OK {
		out = append(out, Finding{Level: Warn, Code: GrainMismatch, Index: i})
	}

	if rung.Calibrated {
		out = append(out, Finding{Level: Info, Code: Calibrated, Index: i, Limit: ladder.CalibrationTolerance})
	}

	return out
}
