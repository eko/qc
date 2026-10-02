package htmlreport

import (
	"cmp"
	"fmt"
	"path/filepath"
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
	Code  findings.Code
	// Blocking findings fail the report (findings.Finding.Blocking).
	Blocking bool
	// Scope names the report the finding comes from on combined pages.
	Scope string
	Text  string
	Spans []span
	// Topic is the section showing the spans; Anchor its id and Where its
	// title, set by link.
	Topic  findings.Topic
	Anchor string
	Where  string
}

// filterBlocking is the filter of blocking findings, next to the level
// names.
const filterBlocking = "blocking"

// Filter is the kind of the finding for the list's filters: blocking, or
// its level.
func (f finding) Filter() string {
	if f.Blocking {
		return filterBlocking
	}

	return f.Level.String()
}

// LevelLabel names the kind of the finding in the list.
func (f finding) LevelLabel() string {
	switch {
	case f.Blocking:
		return "Blocking"
	case f.Level == findings.Warn:
		return "Warning"
	case f.Level == findings.OK:
		return "Passed"
	}

	return "Note"
}

// Icon is the symbol of the finding's kind.
func (f finding) Icon() string {
	switch {
	case f.Blocking:
		return iconFail
	case f.Level == findings.Warn:
		return iconWarn
	case f.Level == findings.OK:
		return iconPass
	}

	return iconInfo
}

// worded is a finding worded as text, with its level and topic.
func worded(
	f findings.Finding,
	spans []span,
	format string,
	args ...any,
) finding {
	return finding{Level: f.Level, Code: f.Code, Blocking: f.Blocking(), Topic: f.Topic, Spans: spans, Text: fmt.Sprintf(format, args...)}
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

	if w, ok := motionFinding(f); ok {
		return w, true
	}

	if w, ok := audioFinding(f, r); ok {
		return w, true
	}

	return hdrFinding(f)
}

// hdrFinding words an HDR finding, if f is one.
func hdrFinding(
	f findings.Finding,
) (finding, bool) {
	text := hdrFindingText(f)

	return worded(f, nil, "%s", text), text != ""
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

	if w, ok := hdrFinding(f); ok {
		return []finding{w}
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
		if w, ok := ladderFinding(f, r); ok {
			out = append(out, w)
		}
	}

	return out
}

// programFinding words a finding of the ladder of a program, read video
// by video; ok is false for the other findings.
func programFinding(
	f findings.Finding,
	res *ladder.Result,
) (finding, bool) {
	switch f.Code {
	case findings.TitleBelowProgram:
		return worded(f, nil, "%s: VMAF %.1f on the top rung for %.1f over the program. The shared ladder under-serves it: a ladder of its own would reach the target",
			sourceName(res, f.Index), f.Value, f.Limit), true
	case findings.TitleAboveProgram:
		return worded(f, nil, "%s: VMAF %.1f on the top rung for %.1f over the program. It would reach the target with fewer bits on a ladder of its own",
			sourceName(res, f.Index), f.Value, f.Limit), true
	case findings.ProgramEven:
		return worded(f, nil, "Every video within %.1f VMAF of the program on the top rung", f.Value), true
	case findings.ProgramSpread:
		titles := res.Rungs[f.Index].Measured.Titles
		_, high, _ := findings.Spread(res.Rungs[f.Index].Measured)

		return worded(f, nil, "Rung %d (%dp): %.1f VMAF between %s (%.1f) and %s (%.1f). One CRF for all does not give the videos one quality down the ladder",
			f.Index+1, res.Rungs[f.Index].Height, f.Value, sourceName(res, f.Other), titles[f.Other].VMAF, sourceName(res, high), titles[high].VMAF), true
	}

	return finding{}, false
}

// ladderFinding words a finding of a ladder.
func ladderFinding(
	f findings.Finding,
	res *ladder.Result,
) (finding, bool) {
	if f.Code == findings.NoRungs {
		return worded(f, nil, "No rung could be selected"), true
	}

	if w, ok := programFinding(f, res); ok {
		return w, true
	}

	r := res.Rungs[f.Index]

	switch f.Code {
	case findings.RenditionQuality:
		rd := res.Renditions[f.Other]

		return worded(f, nil, "%s on the whole title: VMAF %s, %+.1f from its prediction on the digest",
			rd.Name(), rd.Checked.VMAFLabel(), f.Value), true
	case findings.RenditionBitrate:
		rd := res.Renditions[f.Other]

		return worded(f, nil, "%s: %+.0f%% bitrate over the whole title against the ladder's: declare its measured %s in the manifest (Apple HLS: within %.0f%%)",
			rd.Name(), f.Value*100, bitrateLabel(float64(rd.Bitrate)), f.Limit*100), true
	}

	switch f.Code {
	case findings.TopVMAFMissed:
		return worded(f, nil, "Top rung at VMAF %.1f (%dp), below the target %.0f", f.Value, r.Height, f.Limit), true
	case findings.TopVMAFReached:
		return worded(f, nil, "VMAF %.1f at %s (%dp): target %.0f reached", f.Value, bitrateLabel(float64(rungBitrate(r))), r.Height, f.Limit), true
	case findings.LighterThanApple:
		return worded(f, nil, "Top rung %.0f%% lighter than Apple's static 1080p rung (%.1f Mb/s)", f.Value*100, f.Limit/bitsPerMegabit), true
	case findings.Verification:
		return worded(f, nil, "Verification: measured VMAF within %.1f of the prediction on every rung", f.Value), true
	case findings.TopDigest:
		return worded(f, nil, "Estimated on the most complex scenes of the title (TI %.1f for %.1f over the title): the bitrates are what those scenes need, not the title's average, and codecs compare as they do on those scenes",
			f.Value, f.Limit), true
	case findings.Extrapolated:
		return worded(f, nil, "%dp rung targets a quality outside the probed range of that resolution: trust the measured value", r.Height), true
	case findings.GrainMismatch:
		g := r.Grain
		return worded(f, nil, "%dp rung: synthesised grain at %.0f%% of the source's (σ %.2f vs %.2f)", r.Height, g.Ratio*100, g.Output.Sigma, g.Source.Sigma), true
	case findings.Calibrated:
		return worded(f, nil, "%dp rung missed its prediction: CRF corrected and re-measured", r.Height), true
	case findings.PerShotRejected:
		return worded(f, nil, "Rung %d (%dp): no per-shot version, the one tried cost %.1f%% more than the rung at equal VMAF on the digest",
			f.Index+1, r.Height, f.Value*100), true
	case findings.BandedRung:
		return worded(f, nil, "Rung %d (%dp): visible banding on %.0f%% of the scored frames (CAMBI > %.0f): a 10-bit encode fixes it better than more bitrate",
			f.Index+1, r.Height, f.Value*100, f.Limit), true
	case findings.RankConflict:
		lo := res.Rungs[f.Other]
		return worded(f, nil, "Rungs %d (%dp) and %d (%dp): VMAF ranks %dp higher (%.1f vs %.1f) but XPSNR ranks it lower (%.2f vs %.2f dB)",
			f.Index+1, r.Height, f.Other+1, lo.Height, r.Height, r.Measured.VMAF, lo.Measured.VMAF,
			r.Measured.Metrics[quality.SeriesXPSNRY], lo.Measured.Metrics[quality.SeriesXPSNRY]), true
	}

	return hdrFinding(f)
}

// sourceName is the file name of video i of a program.
func sourceName(
	res *ladder.Result,
	i int,
) string {
	return filepath.Base(res.Sources[i].Info.Path)
}

// codecFindings words how the ladders of a run compare at equal quality.
func codecFindings(
	ladders []*ladder.Result,
) []finding {
	var out []finding

	for _, f := range findings.Codecs(ladders) {
		l, ref := ladders[f.Index], ladders[f.Other]

		gap, _ := ladder.CompareRates(ref, l)
		text := fmt.Sprintf("%s needs %s bitrate than %s at VMAF %.1f, the highest quality both ladders reach (%s on average from VMAF %.0f)",
			l.Codec.Name, rateShare(gap.Top), ref.Codec.Name, gap.VMAF, rateShare(gap.Mean), gap.Low)

		if f.Code == findings.CodecCostlier {
			text += fmt.Sprintf("; %s at VMAF %.0f at worst. A newer codec is expected to need less: check the encoder preset, what the digest holds, and the other metrics of the rungs (VMAF v1 counts chroma, which encoders weigh differently)",
				rateShare(gap.Worst), gap.WorstVMAF)
		}

		out = append(out, worded(f, nil, "%s", text))
	}

	return out
}

// rateShare words a bitrate gap: "12% less", "7% more".
func rateShare(
	gap float64,
) string {
	if gap < 0 {
		return fmt.Sprintf("%.0f%% less", -gap*100)
	}

	return fmt.Sprintf("%.0f%% more", gap*100)
}

// bitsPerMegabit converts bitrates to Mb/s.
const bitsPerMegabit = 1e6

// rungBitrate is the bitrate of rung r: measured when it was verified,
// planned otherwise.
func rungBitrate(
	r ladder.Rung,
) int64 {
	if r.Measured != nil {
		return r.Measured.Bitrate
	}

	return r.Bitrate
}
