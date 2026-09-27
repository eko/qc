package findings

import (
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

// Codes of a quality comparison.
const (
	// Fallback: an exact measurement replaced sampling. Text is the reason.
	Fallback Code = "fallback"
	// BudgetClamped: a fixed sampling budget was adapted to the video. Text
	// says how (quality.SampleReport.Clamped).
	BudgetClamped Code = "budget-clamped"
	// WorstFrame: a scored frame far below the mean, which the mean hides.
	// Index is its frame number, Value its score, Spans its instant.
	WorstFrame Code = "worst-frame"
	// Banding: CAMBI shows visible banding. Spans are the banded segments
	// (quality.Banding.Segments), Limit the CAMBI threshold.
	Banding Code = "banding"
	// NoBanding: CAMBI stays under the threshold on every scored frame.
	// Limit is the threshold.
	NoBanding Code = "no-banding"
	// SampledOnly: the extremes (min, 5th percentile) come from sampled
	// frames only.
	SampledOnly Code = "sampled-only"
)

// WorstFrameMargin is how far below the mean (VMAF) a scored frame must be
// to be reported: an isolated drop that the mean hides.
const WorstFrameMargin = 15

// Comparison lists the findings of a quality measurement.
func Comparison(
	v *quality.Result,
) []Finding {
	var out []Finding

	if f, ok := hdrComparison(v.HDR); ok {
		out = append(out, f)
	}

	if v.Fallback != "" {
		out = append(out, Finding{Level: Info, Code: Fallback, Text: v.Fallback})
	}

	if v.Sample != nil && v.Sample.Clamped != "" {
		out = append(out, Finding{Level: Info, Code: BudgetClamped, Text: v.Sample.Clamped})
	}

	if worst, ok := worstFrame(v.Frames); ok && worst.Score < v.Mean-WorstFrameMargin {
		out = append(out, Finding{
			Level: Warn, Code: WorstFrame, Topic: TopicQuality,
			Index: worst.Index, Value: worst.Score, Spans: []media.Interval{{Start: worst.PTS, End: worst.PTS}},
		})
	}

	if b := v.Banding; b != nil {
		out = append(out, bandingFinding(b))
	}

	if v.Mode == quality.ModeSampled {
		out = append(out, Finding{Level: Info, Code: SampledOnly})
	}

	return out
}

// bandingFinding reports the segments where CAMBI shows visible banding, or
// that there is none.
func bandingFinding(
	b *quality.Banding,
) Finding {
	if len(b.Segments) == 0 {
		return Finding{Level: OK, Code: NoBanding, Limit: b.Threshold}
	}

	spans := make([]media.Interval, len(b.Segments))
	for i, s := range b.Segments {
		spans[i] = s.Interval
	}

	return Finding{Level: Warn, Code: Banding, Topic: TopicBanding, Spans: spans, Limit: b.Threshold}
}

// worstFrame is the lowest scored frame, the first one on ties.
func worstFrame(
	frames []quality.FrameScore,
) (quality.FrameScore, bool) {
	if len(frames) == 0 {
		return quality.FrameScore{}, false
	}

	worst := frames[0]
	for _, f := range frames[1:] {
		if f.Score < worst.Score {
			worst = f
		}
	}

	return worst, true
}
