package htmlreport

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/internal/htmlreport/svg"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

// The rules behind the findings live in internal/findings; this file only
// words them for the page, linking their time ranges to the charts.

// maxSegmentFindings bounds the segments listed or linked one by one.
const maxSegmentFindings = 12

// span is a time range, in seconds; From == To is an instant.
type span struct {
	From, To float64
}

// spanOf is the span of a media interval.
func spanOf(
	iv media.Interval,
) span {
	return span{iv.Start.Seconds(), iv.End.Seconds()}
}

// Label is the range as h:mm:ss.mmm.
func (s span) Label() string {
	if s.To <= s.From {
		return svg.UnitTime.Format(s.From)
	}

	return svg.UnitTime.Format(s.From) + " – " + svg.UnitTime.Format(s.To)
}

// Attr formats a bound for a data attribute.
func (span) Attr(
	v float64,
) string {
	return strconv.FormatFloat(v, 'f', svg.TimeDigits, 64)
}

// finding is a finding as the page lists it, with the time ranges it links.
type finding struct {
	Level findings.Level
	// Scope names the report the finding comes from on combined pages.
	Scope string
	Text  string
	Spans []span
	// Topic is the section showing the spans; Anchor its id, set by link.
	Topic  findings.Topic
	Anchor string
}

// LevelLabel names the level of the finding in the list.
func (f finding) LevelLabel() string {
	switch f.Level {
	case findings.Warn:
		return "Warning"
	case findings.OK:
		return "Passed"
	}

	return "Note"
}

// worded is a finding worded as text, with its level and topic.
func worded(
	f findings.Finding,
	spans []span,
	format string,
	args ...any,
) finding {
	return finding{Level: f.Level, Topic: f.Topic, Spans: spans, Text: fmt.Sprintf(format, args...)}
}

// sortFindings puts warnings first, then notes, then good news, keeping
// the order within a level.
func sortFindings(
	list []finding,
) []finding {
	out := slices.Clone(list)
	slices.SortStableFunc(out, func(a, b finding) int { return cmp.Compare(a.Level, b.Level) })

	return out
}

func analysisFindings(
	r *analysis.Report,
) []finding {
	var out []finding

	for _, f := range findings.Analysis(r) {
		if w, ok := analysisFinding(f, r); ok {
			out = append(out, w)
		}
	}

	return out
}

// analysisFinding words a finding of a technical analysis.
func analysisFinding(
	f findings.Finding,
	r *analysis.Report,
) (finding, bool) {
	switch f.Code {
	case findings.PeakBitrate:
		return worded(f, nil, "Peak bitrate is %.1f× the average (HLS recommends ≤ %.0f×)", f.Value, f.Limit), true
	case findings.KeyframeInterval:
		return worded(f, nil, "Keyframes up to %.1fs apart: slow seeking and long ABR segments", f.Value), true
	case findings.BlackSegments:
		return segmentFinding(f, "Black picture"), true
	case findings.FrozenSegments:
		return segmentFinding(f, "Frozen picture"), true
	case findings.BlackBars:
		c := r.Video.Crop.Content
		return worded(f, nil, "Picture content is %d×%d at +%d+%d (black bars): crop before encoding", c.Width, c.Height, c.X, c.Y), true
	case findings.LevelsOutOfRange:
		levels := r.Video.Levels.Levels
		return worded(f, nil, "%.1f%% of luma samples outside %d–%d on the worst frames%s", f.Value*100, levels.Black, levels.White, assumedRange(r)), true
	case findings.NoBlackOrFrozen:
		return worded(f, nil, "No black or frozen segment"), true
	case findings.Interlaced:
		return worded(f, nil, "Interlaced source (%s)", f.Text), true
	}

	return finding{}, false
}

// segmentFinding holds every black or frozen segment in one finding, the
// first ones linked.
func segmentFinding(
	f findings.Finding,
	what string,
) finding {
	spans := make([]span, 0, min(len(f.Spans), maxSegmentFindings))
	for _, s := range f.Spans[:min(len(f.Spans), maxSegmentFindings)] {
		spans = append(spans, spanOf(s))
	}

	text := fmt.Sprintf("%s: %s", what, plural(len(f.Spans), "segment"))
	if more := len(f.Spans) - len(spans); more > 0 {
		text += fmt.Sprintf(" (first %d linked)", len(spans))
	}

	return worded(f, spans, "%s", text)
}

// assumedRange notes that levels were checked against the limited range
// because the stream does not signal its own.
func assumedRange(
	r *analysis.Report,
) string {
	if v, _ := r.Info.PrimaryVideo(); v.Color.Range == "" {
		return " (range not signalled, limited assumed)"
	}

	return ""
}

func comparisonFindings(
	v *quality.Result,
) []finding {
	var out []finding

	for _, f := range findings.Comparison(v) {
		out = append(out, comparisonFinding(f, v)...)
	}

	return out
}

// comparisonFinding words a finding of a quality measurement. A clamped
// budget is a note of the quality section rather than a finding.
func comparisonFinding(
	f findings.Finding,
	v *quality.Result,
) []finding {
	switch f.Code {
	case findings.Fallback:
		return []finding{worded(f, nil, "%s", f.Text)}
	case findings.WorstFrame:
		return []finding{worded(f, []span{spanOf(f.Spans[0])}, "Worst scored frame %d: VMAF %.1f", f.Index, f.Value)}
	case findings.Banding:
		return bandingFindings(f, v.Banding)
	case findings.NoBanding:
		return []finding{worded(f, nil, "No visible banding: CAMBI ≤ %.0f on every scored frame", f.Limit)}
	case findings.SampledOnly:
		return []finding{worded(f, nil, "Min and 5th percentile come from sampled frames only: use --exact for quality gates")}
	}

	return nil
}

// bandingFindings lists the first banded segments one by one, each linked,
// then how many more the Banding section lists.
func bandingFindings(
	f findings.Finding,
	b *quality.Banding,
) []finding {
	var out []finding

	for _, s := range b.Segments[:min(len(b.Segments), maxSegmentFindings)] {
		out = append(out, worded(f, []span{spanOf(s.Interval)}, "Visible banding on %s (CAMBI peak %.1f)", plural(s.Frames, "scored frame"), s.Peak))
	}

	if more := len(b.Segments) - maxSegmentFindings; more > 0 {
		out = append(out, worded(f, nil, "%d more banded segments in the Banding section", more))
	}

	return out
}

func ladderFindings(
	r *ladder.Result,
) []finding {
	var out []finding

	for _, f := range findings.Ladder(r) {
		if w, ok := ladderFinding(f, r.Rungs); ok {
			out = append(out, w)
		}
	}

	return out
}

// ladderFinding words a finding of a ladder.
func ladderFinding(
	f findings.Finding,
	rungs []ladder.Rung,
) (finding, bool) {
	if f.Code == findings.NoRungs {
		return worded(f, nil, "No rung could be selected"), true
	}

	r := rungs[f.Index]

	switch f.Code {
	case findings.TopVMAFMissed:
		return worded(f, nil, "The title never reaches VMAF %.0f at %dp: top rung at the best probed quality", f.Limit, r.Height), true
	case findings.TopVMAFReached:
		return worded(f, nil, "VMAF %.0f reached at %s (%dp)", f.Limit, bitrateLabel(float64(r.Bitrate)), r.Height), true
	case findings.LighterThanApple:
		return worded(f, nil, "Top rung %.0f%% lighter than Apple's static 1080p rung (%.1f Mb/s)", f.Value*100, f.Limit/bitsPerMegabit), true
	case findings.Verification:
		return worded(f, nil, "Verification: measured VMAF within %.1f of the prediction on every rung", f.Value), true
	case findings.Extrapolated:
		return worded(f, nil, "%dp rung targets a quality outside the probed range of that resolution: trust the measured value", r.Height), true
	case findings.GrainMismatch:
		g := r.Grain
		return worded(f, nil, "%dp rung: synthesised grain at %.0f%% of the source's (σ %.2f vs %.2f)", r.Height, g.Ratio*100, g.Output.Sigma, g.Source.Sigma), true
	case findings.Calibrated:
		return worded(f, nil, "%dp rung missed its prediction: CRF corrected and re-measured", r.Height), true
	case findings.BandedRung:
		return worded(f, nil, "Rung %d (%dp): visible banding on %.0f%% of the scored frames (CAMBI > %.0f): a 10-bit encode fixes it better than more bitrate",
			f.Index+1, r.Height, f.Value*100, f.Limit), true
	case findings.RankConflict:
		lo := rungs[f.Other]
		return worded(f, nil, "Rungs %d (%dp) and %d (%dp): VMAF ranks %dp higher (%.1f vs %.1f) but XPSNR ranks it lower (%.2f vs %.2f dB)",
			f.Index+1, r.Height, f.Other+1, lo.Height, r.Height, r.Measured.VMAF, lo.Measured.VMAF,
			r.Measured.Metrics[quality.SeriesXPSNRY], lo.Measured.Metrics[quality.SeriesXPSNRY]), true
	}

	return finding{}, false
}

// bitsPerMegabit converts bitrates to Mb/s.
const bitsPerMegabit = 1e6
