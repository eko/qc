package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/audio"
	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/media"
)

// sparkFloor clamps the short-term loudness of the sparkline: quieter is
// below the gate of BS.1770, and silence (loudness.Floor) would flatten
// the programme.
const sparkFloor = -70.0

// audioSection sums up the audio tracks: loudness and true peak against
// the target, loudness range, and the short-term loudness over time. It
// is empty without an audio analysis.
func audioSection(
	report *analysis.Report,
	width int,
) string {
	a := report.Audio
	if a == nil || len(a.Tracks) == 0 {
		return ""
	}

	lines := []string{section.Render("Audio") + "  " + stat("target", targetLabel(a.Target))}

	for i := range a.Tracks {
		t := &a.Tracks[i]
		lines = append(lines, trackLine(t, streamInfo(report.Info, t.Stream)))

		if t.Loudness.Integrated > loudness.Floor {
			lines = append(lines, rowLabel("short-term")+Sparkline(clampLow(t.Loudness.Series.ShortTerm, sparkFloor), width-gutter, Mean, Accent))
		}
	}

	return strings.Join(lines, "\n")
}

// targetLabel describes a loudness target.
func targetLabel(
	t loudness.Target,
) string {
	return fmt.Sprintf("%s %g LUFS ±%g · TP ≤ %g dBTP", t.Name, t.Integrated, t.Tolerance, t.MaxTruePeak)
}

// trackLine is a track's loudness, range and true peak, marked against the
// target.
func trackLine(
	t *audio.Track,
	stream media.AudioStream,
) string {
	name := fmt.Sprintf("  #%-2d %s %s %s", t.Stream, stream.Codec, t.Layout, rateLabel(stream.SampleRate))
	if stream.Language != "" {
		name += " " + stream.Language
	}

	l, c := t.Loudness, t.Compliance
	if l.Integrated <= loudness.Floor {
		return Bold.Render(name) + "   " + Red.Render("silent")
	}

	return Bold.Render(name) + "   " +
		stat("I", fmt.Sprintf("%.1f LUFS %s", l.Integrated, loudnessMark(c))) +
		stat("LRA", fmt.Sprintf("%.1f LU", l.Range)) +
		stat("TP", fmt.Sprintf("%.1f dBTP %s", l.TruePeak, peakMark(c))) +
		stat("max M/S", fmt.Sprintf("%.1f / %.1f", l.MaxMomentary, l.MaxShortTerm))
}

// loudnessMark is ✓ on target, ▲ too loud, ▼ too quiet.
func loudnessMark(
	c loudness.Compliance,
) string {
	switch {
	case c.Loudness:
		return Green.Render("✓")
	case c.Deviation > 0:
		return Red.Render("▲")
	}

	return Red.Render("▼")
}

// peakMark is ✓ under the ceiling, ▲ over it.
func peakMark(
	c loudness.Compliance,
) string {
	if c.TruePeak {
		return Green.Render("✓")
	}

	return Red.Render("▲")
}

// rateLabel formats a sample rate in kHz.
func rateLabel(
	rate int,
) string {
	return fmt.Sprintf("%g kHz", float64(rate)/1000)
}

// streamInfo returns the description of an audio stream index.
func streamInfo(
	info *media.Info,
	index int,
) media.AudioStream {
	for _, a := range info.Audio {
		if a.Index == index {
			return a
		}
	}

	return media.AudioStream{Index: index}
}

// clampLow raises the values under floor to it.
func clampLow(
	values []float64,
	floor float64,
) []float64 {
	out := make([]float64, len(values))
	for i, v := range values {
		out[i] = max(v, floor)
	}

	return out
}

// audioLines words a finding of the audio analysis, or returns nil for
// another finding. Segments get a line each.
func audioLines(
	f findings.Finding,
	report *analysis.Report,
) []string {
	if report.Audio == nil || f.Index < 0 || f.Index >= len(report.Audio.Tracks) {
		return nil
	}

	track := fmt.Sprintf("audio #%d", report.Audio.Tracks[f.Index].Stream)

	switch f.Code {
	case findings.AudioSilence:
		return spanLines(f, track+": silence")
	case findings.ChannelSilence:
		return spanLines(f, fmt.Sprintf("%s: %s silent while the others play,", track, f.Text))
	case findings.OutOfPhase:
		return spanLines(f, fmt.Sprintf("%s: %s out of phase", track, f.Text))
	}

	if text := audioFindingText(f, report.Audio.Target); text != "" {
		return []string{findingLine(f.Level, "%s%s", track, text)}
	}

	return nil
}

// spanLines lists the spans of a finding, one line each.
func spanLines(
	f findings.Finding,
	what string,
) []string {
	lines := make([]string, len(f.Spans))
	for i, s := range f.Spans {
		lines[i] = findingLine(f.Level, "%s from %s to %s", what, Clock(s.Start, false), Clock(s.End, false))
	}

	return lines
}

// audioFindingText words the audio findings holding in one line, after the
// track's name.
func audioFindingText(
	f findings.Finding,
	target loudness.Target,
) string {
	if text := loudnessText(f, target); text != "" {
		return text
	}

	switch f.Code {
	case findings.SilentTrack:
		if f.Value <= loudness.Floor {
			return " is silent (digital zero)"
		}

		return fmt.Sprintf(" is silent (peak %.1f dBFS)", f.Value)
	case findings.LeadingSilence:
		return fmt.Sprintf(" starts with %.1fs of silence", f.Value)
	case findings.TrailingSilence:
		return fmt.Sprintf(" ends with %.1fs of silence", f.Value)
	case findings.MutedChannel:
		return fmt.Sprintf(": %s is muted (silent while the others play)", f.Text)
	case findings.EmptyLFE:
		return ": the LFE channel is empty"
	case findings.EmptyCentre:
		return ": the centre channel is empty, where dialogue usually is"
	case findings.Clipping:
		return fmt.Sprintf(": %.0f clipped samples (%s) in %d segment(s), first at %s", f.Value, f.Text, len(f.Spans), firstSpan(f))
	case findings.DCOffset:
		return fmt.Sprintf(": %s has a DC offset of %.2f%% of full scale (> %.2f%%)", f.Text, f.Value*100, f.Limit*100)
	case findings.InvertedPolarity:
		return fmt.Sprintf(": %s in opposite polarity (correlation %.2f): one channel is inverted, the mix cancels in mono", f.Text, f.Value)
	case findings.MonoAsStereo:
		return fmt.Sprintf(": %s carry the same signal (mono as stereo)", f.Text)
	}

	return formatText(f)
}

// loudnessText words the loudness findings.
func loudnessText(
	f findings.Finding,
	target loudness.Target,
) string {
	switch f.Code {
	case findings.LoudnessOffTarget:
		direction := "above"
		if f.Value < f.Limit {
			direction = "below"
		}

		return fmt.Sprintf(": %.1f LUFS, %.1f LU %s the %s target (%g ±%g)", f.Value, math.Abs(f.Value-f.Limit), direction, f.Text, f.Limit, target.Tolerance)
	case findings.LoudnessOnTarget:
		return fmt.Sprintf(": %.1f LUFS, true peak within %g dBTP: meets the %s target", f.Value, target.MaxTruePeak, f.Text)
	case findings.TruePeakOver:
		return fmt.Sprintf(": true peak %.1f dBTP over the %g dBTP ceiling (%d time(s), first at %s)", f.Value, f.Limit, len(f.Spans), firstSpan(f))
	}

	return ""
}

// formatText words the findings on the stream's format.
func formatText(
	f findings.Finding,
) string {
	switch f.Code {
	case findings.AudioSampleRate:
		if f.Level == findings.Warn {
			return fmt.Sprintf(" at %g kHz: audio bandwidth under %g kHz", f.Value/1000, f.Value/2000)
		}

		return fmt.Sprintf(" at %g kHz: video and broadcast delivery use 48 kHz", f.Value/1000)
	case findings.AudioBitDepth:
		return fmt.Sprintf(" coded on %.0f bits: below 16-bit delivery quality", f.Value)
	case findings.AudioLayoutGuessed:
		return fmt.Sprintf(" does not signal its channel layout: %s assumed", f.Text)
	case findings.AudioOffset:
		return fmt.Sprintf(" starts %.3fs %s the video (stream start times, not a lip-sync measurement)", math.Abs(f.Value), laterOrEarlier(f.Value))
	}

	return ""
}

// laterOrEarlier words the sign of an offset.
func laterOrEarlier(
	offset float64,
) string {
	if offset > 0 {
		return "after"
	}

	return "before"
}

// firstSpan is the start of a finding's first span, or a dash.
func firstSpan(
	f findings.Finding,
) string {
	if len(f.Spans) == 0 {
		return "–"
	}

	return Clock(f.Spans[0].Start, false)
}
