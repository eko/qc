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
	// TopVMAFMissed: the top rung stays below the targeted quality. Value
	// is its quality (verified when it was), Limit the target.
	TopVMAFMissed Code = "top-vmaf-missed"
	// TopVMAFReached: the top rung reaches the targeted quality. Value is
	// its quality (verified when it was), Limit the target.
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
	// (ladder.CalibrationTolerance, ladder.TopCalibrationTolerance for the
	// top rung) and had its CRF corrected.
	Calibrated Code = "calibrated"
	// BandedRung: the rung shows visible banding on a share of its scored
	// frames (Value) with CAMBI above Limit.
	BandedRung Code = "banded-rung"
	// BandedSource: like BandedRung, on frames mostly banded in the source
	// already (InheritedBandingShare of them or more, see
	// ladder.Measurement.InheritedBanding): the encode did not make that
	// banding, and neither bitrate nor bit depth removes it.
	BandedSource Code = "banded-source"
	// RankConflict: VMAF ranks rung Index above rung Other, XPSNR below
	// (ladder.RankConflicts).
	RankConflict Code = "rank-conflict"
	// RenditionQuality: rendition Other (of ladder.Result.Renditions), of
	// rung Index, measured on the whole title, strays from the quality
	// predicted on the digest by Value (VMAF), beyond its confidence
	// interval plus Limit.
	RenditionQuality Code = "rendition-quality"
	// PerShotRejected: the per-shot version of the rung was dropped, its
	// verification on the digest costing Value (a share) more than the
	// rung at equal VMAF (ladder.Rung.PerShotRejected).
	PerShotRejected Code = "per-shot-rejected"
	// TitleBelowProgram: on the top rung of the ladder of a program, video
	// Index (of ladder.Result.Sources) scores Value where the program
	// scores Limit, more than ProgramTolerance below: the shared ladder
	// under-serves it, and a ladder of its own would reach the target.
	TitleBelowProgram Code = "title-below-program"
	// TitleAboveProgram: video Index scores Value on the top rung where
	// the program scores Limit, more than ProgramTolerance above: it would
	// reach the target with fewer bits than the shared ladder gives it.
	TitleAboveProgram Code = "title-above-program"
	// ProgramEven: every video of the program is within Limit
	// (ProgramTolerance) of the top rung's quality; Value is the largest
	// gap.
	ProgramEven Code = "program-even"
	// ProgramSpread: below the top rung, rung Index is where the videos of
	// the program differ most: Value VMAF points between the lowest (video
	// Other) and the highest (see Spread), more than Limit
	// (ProgramSpreadLimit). One CRF for all does not give them one quality.
	ProgramSpread Code = "program-spread"
	// TopDigest: the ladder was estimated on the most complex scenes of the
	// title (ladder.DigestTop), whose temporal information is Value for
	// Limit over the title: its bitrates are those scenes', not the title's.
	TopDigest Code = "top-digest"
	// RenditionBitrate: rendition Other, of rung Index, costs Value (a
	// share) more or less over the whole title than predicted on the
	// digest, beyond Limit: the manifest must declare its measured bitrate.
	RenditionBitrate Code = "rendition-bitrate"
)

const (
	// renditionTolerance is the VMAF a rendition may stray from its
	// prediction beyond its confidence interval: the digest's
	// representativeness (0.86 VMAF on average on a 10-minute title, see
	// docs/validation.md) plus the prediction's own error.
	renditionTolerance = ladder.CalibrationTolerance
	// bandwidthTolerance is how far a rendition's average bitrate may stray
	// from the one planned: Apple's HLS authoring spec wants the declared
	// AVERAGE-BANDWIDTH within 10% of the measured average.
	bandwidthTolerance = 0.10
)

// InheritedBandingShare is the share of the banded frames of a rung banded
// in the source too from which its banding is said to be the source's.
const InheritedBandingShare = 0.5

// ProgramTolerance is how far (VMAF) the quality of a video on the top
// rung may stray from the program's before the shared ladder is said to
// serve it badly: a third of a rung step, and well beyond what the few
// segments of a video measure it within.
const ProgramTolerance = 2.0

// ProgramSpreadLimit is the VMAF span between the videos of a program on a
// rung beyond which the rung is said to serve them unevenly: a quality
// step of the default ladder, a clearly visible one.
const ProgramSpreadLimit = 6.0

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

	if d := r.Digest; d.Sampling == ladder.DigestTop && d.Complexity != nil {
		out = append(out, Finding{Level: Info, Code: TopDigest, Value: d.Complexity.TI, Limit: d.Complexity.TitleTI})
	}

	for i, rung := range r.Rungs {
		out = append(out, rungFindings(i, rung)...)
	}

	for _, i := range ladder.BandedRungs(r.Rungs) {
		code := BandedRung
		if r.Rungs[i].Measured.InheritedBanding() >= InheritedBandingShare {
			code = BandedSource
		}

		out = append(out, Finding{
			Level: Warn, Code: code, Index: i,
			Value: r.Rungs[i].Measured.BandedShare(), Limit: quality.BandingThreshold,
		})
	}

	for _, conflict := range ladder.RankConflicts(r.Rungs) {
		out = append(out, Finding{Level: Warn, Code: RankConflict, Index: conflict.Higher, Other: conflict.Lower})
	}

	out = append(out, programFindings(r)...)
	out = append(out, renditionFindings(r)...)

	return append(out, hdrLadderFindings(r)...)
}

// programFindings read the ladder of a program video by video: on the top
// rung, those the shared ladder serves badly, or that all are served
// alike; below it, the rung where they differ most when that is more than
// a quality step. A video with no scored frame is not judged.
func programFindings(
	r *ladder.Result,
) []Finding {
	top := r.Rungs[0].Measured
	if top == nil || len(top.Titles) < 2 {
		return nil
	}

	var out []Finding

	worst := 0.0

	for i, t := range top.Titles {
		if t.ScoredFrames == 0 {
			continue
		}

		gap := t.VMAF - top.VMAF
		worst = math.Max(worst, math.Abs(gap))

		switch {
		case gap < -ProgramTolerance:
			out = append(out, Finding{Level: Warn, Code: TitleBelowProgram, Index: i, Value: t.VMAF, Limit: top.VMAF})
		case gap > ProgramTolerance:
			out = append(out, Finding{Level: Info, Code: TitleAboveProgram, Index: i, Value: t.VMAF, Limit: top.VMAF})
		}
	}

	if len(out) == 0 {
		out = []Finding{{Level: OK, Code: ProgramEven, Value: worst, Limit: ProgramTolerance}}
	}

	return append(out, spreadFindings(r)...)
}

// spreadFindings name the rung below the top where the videos of a program
// differ most, when by more than ProgramSpreadLimit.
func spreadFindings(
	r *ladder.Result,
) []Finding {
	widest := Finding{Level: Info, Code: ProgramSpread, Value: ProgramSpreadLimit, Limit: ProgramSpreadLimit}

	for i, rung := range r.Rungs[1:] {
		low, high, ok := Spread(rung.Measured)
		if !ok {
			continue
		}

		if span := rung.Measured.Titles[high].VMAF - rung.Measured.Titles[low].VMAF; span > widest.Value {
			widest.Index, widest.Other, widest.Value = i+1, low, span
		}
	}

	if widest.Index == 0 {
		return nil
	}

	return []Finding{widest}
}

// Spread returns the videos of a program scoring lowest and highest on m,
// among those with scored frames; ok is false with fewer than two.
func Spread(
	m *ladder.Measurement,
) (low, high int, ok bool) {
	if m == nil {
		return 0, 0, false
	}

	low, high = -1, -1
	scored := 0

	for i, t := range m.Titles {
		if t.ScoredFrames == 0 {
			continue
		}

		scored++

		if low < 0 || t.VMAF < m.Titles[low].VMAF {
			low = i
		}

		if high < 0 || t.VMAF > m.Titles[high].VMAF {
			high = i
		}
	}

	return low, high, scored > 1
}

// renditionFindings compare every rendition encoded on the whole title with
// what the ladder predicted on the digest.
func renditionFindings(
	r *ladder.Result,
) []Finding {
	var out []Finding

	for i, rd := range r.Renditions {
		vmaf, bitrate := r.Prediction(rd)

		if c := rd.Checked; c != nil {
			if gap := c.VMAF - vmaf; math.Abs(gap) > c.HalfWidth+renditionTolerance {
				out = append(out, Finding{Level: Warn, Code: RenditionQuality, Index: rd.Rung, Other: i, Value: gap, Limit: renditionTolerance})
			}
		}

		if bitrate > 0 {
			if gap := float64(rd.Bitrate)/bitrate - 1; math.Abs(gap) > bandwidthTolerance {
				out = append(out, Finding{Level: Warn, Code: RenditionBitrate, Index: rd.Rung, Other: i, Value: gap, Limit: bandwidthTolerance})
			}
		}
	}

	return out
}

// topFinding says whether the top rung reaches the targeted quality: as
// verified (on every frame of the digest) when it was, as predicted
// otherwise. Value is that quality.
func topFinding(
	top ladder.Rung,
	target float64,
) Finding {
	quality := top.PredictedVMAF
	if top.Measured != nil {
		quality = top.Measured.VMAF
	}

	if quality < target-topVMAFTolerance {
		return Finding{Level: Warn, Code: TopVMAFMissed, Value: quality, Limit: target}
	}

	return Finding{Level: OK, Code: TopVMAFReached, Value: quality, Limit: target}
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
		limit := ladder.CalibrationTolerance
		if i == 0 {
			limit = ladder.TopCalibrationTolerance
		}

		out = append(out, Finding{Level: Info, Code: Calibrated, Index: i, Limit: limit})
	}

	if ps := rung.PerShotRejected; ps != nil {
		out = append(out, Finding{Level: Info, Code: PerShotRejected, Index: i, Value: -ps.Gain})
	}

	return out
}
