package findings

import (
	"github.com/eko/qc/ladder"
)

// Codes of the ladders of a run, compared with each other. Index is the
// ladder concerned and Other the ladder it is compared with (indices of the
// ladders given to Codecs), Value the bitrate of the first over the
// second's at the highest quality both reach, minus one, and Limit that
// quality (ladder.CompareRates gives the rest: the mean and the worst gap).
const (
	// CodecSaving: the ladder of a newer codec needs less bitrate than the
	// ladder of the oldest codec of the run at equal quality.
	CodecSaving Code = "codec-saving"
	// CodecCostlier: the ladder of a newer codec needs more bitrate than
	// the ladder of the oldest codec of the run at equal quality, at their
	// top quality or on average. Nothing is wrong with the measurements for
	// that, but the result goes against what the codecs are chosen for, and
	// has a cause worth knowing: a preset too fast for the newer encoder,
	// a digest of scenes it handles no better (ladder.DigestTop), or a
	// metric weighing what each encoder keeps differently (VMAF v1 counts
	// chroma, where SVT-AV1 gives less than x264: docs/validation.md).
	CodecCostlier Code = "codec-costlier"
)

// codecTolerance is the bitrate a newer codec may cost over an older one at
// equal quality before it is reported: what two rungs measured within ±1
// VMAF, and read between rungs, cannot tell apart.
const codecTolerance = 0.03

// generations ranks the codecs by age: a newer one is expected to need
// less bitrate than an older one at equal quality.
var generations = map[string]int{"h264": 1, "hevc": 2, "av1": 3}

// Codecs compares the ladders of a run: every ladder of a newer codec with
// the ladder of the oldest codec, at equal quality. It returns nothing for
// a single codec, for unknown codecs, and for ladders sharing no quality.
func Codecs(
	ladders []*ladder.Result,
) []Finding {
	ref := -1

	for i, l := range ladders {
		if g := generations[l.Codec.Name]; g > 0 && (ref < 0 || g < generations[ladders[ref].Codec.Name]) {
			ref = i
		}
	}

	var out []Finding

	for i, l := range ladders {
		if ref < 0 || generations[l.Codec.Name] <= generations[ladders[ref].Codec.Name] {
			continue
		}

		gap, ok := ladder.CompareRates(ladders[ref], l)
		if !ok {
			continue
		}

		f := Finding{Level: Info, Code: CodecSaving, Index: i, Other: ref, Value: gap.Top, Limit: gap.VMAF}
		if gap.Top > codecTolerance || gap.Mean > codecTolerance {
			f.Level, f.Code = Warn, CodecCostlier
		}

		out = append(out, f)
	}

	return out
}
