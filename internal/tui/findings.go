package tui

import (
	"cmp"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/quality"
)

// The rules behind the findings live in internal/findings; this file only
// words them for the terminal: one line each, marked by level.

// maxBandingSegments bounds the banded segments listed in findings.
const maxBandingSegments = 3

// findingsBlock renders a findings block, or nothing when there is none.
func findingsBlock(
	lines []string,
) string {
	if len(lines) == 0 {
		return ""
	}

	return section.Render("Findings") + "\n" + strings.Join(lines, "\n")
}

// findingLine marks a line with the symbol of its level.
func findingLine(
	level findings.Level,
	format string,
	args ...any,
) string {
	text := fmt.Sprintf(format, args...)

	switch level {
	case findings.Warn:
		return warnLine(text)
	case findings.Info:
		return infoLine(text)
	}

	return okLine(text)
}

// reportFindings words the findings of a technical analysis, issues first.
func reportFindings(
	report *analysis.Report,
) []string {
	list := findings.Analysis(report)
	findings.SortByLevel(list)

	var lines []string
	for _, f := range list {
		lines = append(lines, analysisLines(f, report)...)
	}

	return lines
}

// analysisLines words a finding of a technical analysis: segments get a line
// each.
func analysisLines(
	f findings.Finding,
	report *analysis.Report,
) []string {
	line := func(format string, args ...any) []string {
		return []string{findingLine(f.Level, format, args...)}
	}

	switch f.Code {
	case findings.PeakBitrate:
		return line("peak bitrate is %.1f× the average (HLS recommends ≤ %.0f×)", f.Value, f.Limit)
	case findings.KeyframeInterval:
		return line("keyframes up to %.2fs apart: slow seeking and long ABR segments", f.Value)
	case findings.BlackSegments:
		return segmentLines(f, "black")
	case findings.FrozenSegments:
		return segmentLines(f, "frozen picture")
	case findings.BlackBars:
		c := report.Video.Crop.Content
		return line("picture content is %d×%d at +%d+%d (black bars): crop before encoding", c.Width, c.Height, c.X, c.Y)
	case findings.LevelsOutOfRange:
		levels := report.Video.Levels.Levels
		return line("%.1f%% of luma samples outside %d–%d on the worst frames%s", f.Value*100, levels.Black, levels.White, assumedRange(report))
	case findings.NoBlackOrFrozen:
		return line("no black or frozen segment")
	case findings.Interlaced:
		return line("interlaced source (%s)", f.Text)
	}

	if lines := audioLines(f, report); lines != nil {
		return lines
	}

	if text := motionFindingText(f); text != "" {
		return line("%s", text)
	}

	if text := hdrFindingText(f); text != "" {
		return line("%s", text)
	}

	return nil
}

// segmentLines lists every segment of a finding, one line each.
func segmentLines(
	f findings.Finding,
	what string,
) []string {
	lines := make([]string, len(f.Spans))
	for i, s := range f.Spans {
		lines[i] = findingLine(f.Level, "%s from %s to %s", what, Clock(s.Start, false), Clock(s.End, false))
	}

	return lines
}

// assumedRange notes that levels were checked against the limited range
// because the stream does not signal its own.
func assumedRange(
	report *analysis.Report,
) string {
	if v, _ := report.Info.PrimaryVideo(); v.Color.Range == "" {
		return " (range not signalled, limited assumed)"
	}

	return ""
}

// comparisonFindings words the findings of a quality measurement, banding
// first: it is the one about the picture rather than the measurement.
func comparisonFindings(
	v *quality.Result,
) []string {
	list := findings.Comparison(v)
	slices.SortStableFunc(list, func(a, b findings.Finding) int {
		return cmp.Compare(bandingRank(a.Code), bandingRank(b.Code))
	})

	lines := make([]string, 0, len(list))
	for _, f := range list {
		if line := comparisonLine(f, v); line != "" {
			lines = append(lines, line)
		}
	}

	return lines
}

// bandingRank puts the banding findings first.
func bandingRank(
	code findings.Code,
) int {
	if code == findings.Banding || code == findings.NoBanding {
		return 0
	}

	return 1
}

// comparisonLine words a finding of a quality measurement.
func comparisonLine(
	f findings.Finding,
	v *quality.Result,
) string {
	switch f.Code {
	case findings.Fallback:
		return findingLine(f.Level, "%s", f.Text)
	case findings.BudgetClamped:
		return findingLine(f.Level, "budget %s: %s", v.Sample.String(), f.Text)
	case findings.WorstFrame:
		return findingLine(f.Level, "worst scored frame at %s: VMAF %.1f", Clock(f.Spans[0].Start, false), f.Value)
	case findings.Drops:
		return findingLine(f.Level, "%.0f%% of the frames score more than %.0f VMAF under the mean (below %.1f): the mean hides them, the harmonic mean is %.1f",
			f.Value*100, v.Drops.Margin, f.Limit, v.HarmonicMean)
	case findings.Banding:
		return bandingLine(f, v.Banding)
	case findings.NoBanding:
		return findingLine(f.Level, "no visible banding: CAMBI ≤ %.0f on every scored frame", f.Limit)
	case findings.SampledOnly:
		return findingLine(f.Level, "min/p5 come from sampled frames only; use --exact for quality gates")
	}

	if text := hdrFindingText(f); text != "" {
		return findingLine(f.Level, "%s", text)
	}

	return ""
}

// bandingLine lists the first banded segments on one line.
func bandingLine(
	f findings.Finding,
	b *quality.Banding,
) string {
	parts := make([]string, 0, maxBandingSegments+1)
	for _, s := range b.Segments[:min(len(b.Segments), maxBandingSegments)] {
		parts = append(parts, fmt.Sprintf("%s → %s (peak %.1f)", Clock(s.Start, false), Clock(s.End, false), s.Peak))
	}

	if more := len(b.Segments) - maxBandingSegments; more > 0 {
		parts = append(parts, fmt.Sprintf("%d more", more))
	}

	return findingLine(f.Level, "visible banding (CAMBI > %.0f) on %d scored frames%s: %s", f.Limit, b.BandedFrames, inheritedBanding(b), strings.Join(parts, ", "))
}

// inheritedBanding tells how many of the banded frames are banded in the
// reference too, "" when none is.
func inheritedBanding(
	b *quality.Banding,
) string {
	if b.SourceFrames == 0 {
		return ""
	}

	return fmt.Sprintf(" (%d of them banded in the reference too)", b.SourceFrames)
}

// ladderFindings words the findings of a ladder. The terminal groups the
// rungs' findings by kind, and sums the calibrated rungs up in a last line
// explaining the ✱ of the ladder table.
func ladderFindings(
	res *ladder.Result,
) []string {
	list := findings.Ladder(res)
	slices.SortStableFunc(list, func(a, b findings.Finding) int {
		return cmp.Compare(ladderRank(a.Code), ladderRank(b.Code))
	})

	var (
		lines      []string
		calibrated int
	)

	for _, f := range list {
		if f.Code == findings.Calibrated {
			calibrated++

			continue
		}

		if line := ladderLine(f, res); line != "" {
			lines = append(lines, line)
		}
	}

	if calibrated > 0 {
		lines = append(lines, Yellow.Render("✱ ")+fmt.Sprintf("%d rung(s) missed the prediction by more than %.1f (%.1f for the top rung) and had their CRF corrected (secant step) and re-measured",
			calibrated, ladder.CalibrationTolerance, ladder.TopCalibrationTolerance))
	}

	return lines
}

// ladderRank orders the findings of a ladder by kind: ladder-wide first,
// then the rungs' findings, one kind after the other.
func ladderRank(
	code findings.Code,
) int {
	switch code {
	case findings.RenditionQuality, findings.RenditionBitrate:
		return 6
	case findings.Extrapolated:
		return 1
	case findings.GrainMismatch:
		return 2
	case findings.BandedRung, findings.BandedSource:
		return 3
	case findings.RankConflict:
		return 4
	case findings.Calibrated:
		return 5
	}

	return 0
}

// programLine words a finding of the ladder of a program, read video by
// video; ok is false for the other findings.
func programLine(
	f findings.Finding,
	res *ladder.Result,
) (string, bool) {
	switch f.Code {
	case findings.TitleBelowProgram:
		return findingLine(f.Level, "%s: VMAF %.1f on the top rung for %.1f over the program: the shared ladder under-serves it, a ladder of its own would reach the target",
			sourceName(res, f.Index), f.Value, f.Limit), true
	case findings.TitleAboveProgram:
		return findingLine(f.Level, "%s: VMAF %.1f on the top rung for %.1f over the program: it would reach the target with fewer bits on a ladder of its own",
			sourceName(res, f.Index), f.Value, f.Limit), true
	case findings.ProgramEven:
		return findingLine(f.Level, "every video within %.1f VMAF of the program on the top rung", f.Value), true
	case findings.ProgramSpread:
		titles := res.Rungs[f.Index].Measured.Titles
		_, high, _ := findings.Spread(res.Rungs[f.Index].Measured)

		return findingLine(f.Level, "rung %d (%dp): %.1f VMAF between %s (%.1f) and %s (%.1f): one CRF for all does not give the videos one quality down the ladder",
			f.Index+1, res.Rungs[f.Index].Height, f.Value, sourceName(res, f.Other), titles[f.Other].VMAF, sourceName(res, high), titles[high].VMAF), true
	}

	return "", false
}

// bandedRungLine words the banding of a rung: the encode's, or the one it
// inherited from the source.
func bandedRungLine(
	f findings.Finding,
	r ladder.Rung,
) string {
	if f.Code == findings.BandedSource {
		return findingLine(f.Level, "rung %d (%dp): visible banding on %.0f%% of the scored frames (CAMBI > %.0f), already in the source on %.0f%% of them: neither bitrate nor a 10-bit encode removes it, deband the source",
			f.Index+1, r.Height, f.Value*100, f.Limit, r.Measured.InheritedBanding()*100)
	}

	return findingLine(f.Level, "rung %d (%dp): visible banding on %.0f%% of the scored frames (CAMBI > %.0f): a 10-bit encode fixes it better than more bitrate",
		f.Index+1, r.Height, f.Value*100, f.Limit)
}

// ladderLine words a finding of a ladder.
func ladderLine(
	f findings.Finding,
	res *ladder.Result,
) string {
	if f.Code == findings.NoRungs {
		return findingLine(f.Level, "no rung could be selected")
	}

	if line, ok := programLine(f, res); ok {
		return line
	}

	r := res.Rungs[f.Index]

	switch f.Code {
	case findings.RenditionQuality:
		rd := res.Renditions[f.Other]
		return findingLine(f.Level, "%s on the whole title: VMAF %s, %+.1f from its prediction on the digest",
			renditionName(rd), rd.Checked.VMAFLabel(), f.Value)
	case findings.RenditionBitrate:
		return findingLine(f.Level, "%s: %+.0f%% bitrate over the whole title against the ladder's: declare its measured %s in the manifest (Apple HLS: within %.0f%%)",
			renditionName(res.Renditions[f.Other]), f.Value*100, Bitrate(float64(res.Renditions[f.Other].Bitrate)), f.Limit*100)
	}

	switch f.Code {
	case findings.TopVMAFMissed:
		return findingLine(f.Level, "top rung at VMAF %.1f (%dp), below the target %.0f", f.Value, r.Height, f.Limit)
	case findings.TopVMAFReached:
		return findingLine(f.Level, "VMAF %.1f at %s (%dp): target %.0f reached", f.Value, Bitrate(float64(rungBitrate(r))), r.Height, f.Limit)
	case findings.LighterThanApple:
		return findingLine(f.Level, "top rung %.0f%% lighter than Apple's static 1080p rung (%.1f Mb/s)", f.Value*100, f.Limit/bitsPerMegabit)
	case findings.Verification:
		return findingLine(f.Level, "verification: measured VMAF within %.1f of the prediction on every rung (VBV-capped encodes of the digest)", f.Value)
	case findings.TopDigest:
		return findingLine(f.Level, "estimated on the most complex scenes of the title (TI %.1f for %.1f over the title): the bitrates are what those scenes need, not the title's average",
			f.Value, f.Limit)
	case findings.Extrapolated:
		return findingLine(f.Level, "%dp rung targets a quality outside the probed range of that resolution: its prediction is extrapolated, trust the measured value",
			r.Height)
	case findings.GrainMismatch:
		g := r.Grain
		return findingLine(f.Level, "%dp rung: synthesised grain at %.0f%% of the source's (σ %.2f vs %.2f)", r.Height, g.Ratio*100, g.Output.Sigma, g.Source.Sigma)
	case findings.PerShotRejected:
		return findingLine(f.Level, "rung %d (%dp): no per-shot version, the one tried cost %.1f%% more than the rung at equal VMAF on the digest",
			f.Index+1, r.Height, f.Value*100)
	case findings.BandedRung, findings.BandedSource:
		return bandedRungLine(f, r)
	case findings.RankConflict:
		lo := res.Rungs[f.Other]
		return findingLine(f.Level, "rungs %d (%dp) and %d (%dp): VMAF ranks %dp higher (%.1f vs %.1f) but XPSNR ranks it lower (%.2f vs %.2f dB)",
			f.Index+1, r.Height, f.Other+1, lo.Height, r.Height, r.Measured.VMAF, lo.Measured.VMAF, xpsnr(r), xpsnr(lo))
	}

	if text := hdrFindingText(f); text != "" {
		return findingLine(f.Level, "%s", text)
	}

	return ""
}

// RenderCodecs prints how the ladders of a run compare at equal quality:
// every newer codec against the oldest one. It prints nothing when there is
// nothing to compare (a single codec).
func RenderCodecs(
	w io.Writer,
	ladders []*ladder.Result,
) error {
	list := findings.Codecs(ladders)
	if len(list) == 0 {
		return nil
	}

	lines := []string{reportTitle("codecs at equal quality"), ""}

	for _, f := range list {
		l, ref := ladders[f.Index], ladders[f.Other]

		gap, _ := ladder.CompareRates(ref, l)
		text := fmt.Sprintf("%s needs %s bitrate than %s at VMAF %.1f, the highest quality both ladders reach (%s on average from VMAF %.0f)",
			l.Codec.Name, rateShare(gap.Top), ref.Codec.Name, gap.VMAF, rateShare(gap.Mean), gap.Low)

		if f.Code == findings.CodecCostlier {
			text += fmt.Sprintf("; %s at VMAF %.0f at worst. A newer codec is expected to need less: check the encoder preset, what the digest holds, and the other metrics of the rungs (VMAF v1 counts chroma, which encoders weigh differently)",
				rateShare(gap.Worst), gap.WorstVMAF)
		}

		lines = append(lines, findingLine(f.Level, "%s", text))
	}

	return writeBlocks(w, []string{strings.Join(lines, "\n")})
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

// sourceName is the file name of video i of a program.
func sourceName(
	res *ladder.Result,
	i int,
) string {
	return filepath.Base(res.Sources[i].Info.Path)
}

// xpsnr is the luma XPSNR of a verified rung, which rank conflicts compare.
func xpsnr(
	r ladder.Rung,
) float64 {
	return r.Measured.Metrics[quality.SeriesXPSNRY]
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

// renditionName names a rendition by its file.
func renditionName(
	rd ladder.Rendition,
) string {
	return rd.Name()
}
