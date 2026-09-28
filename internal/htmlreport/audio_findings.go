package htmlreport

import (
	"fmt"
	"math"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/media"
)

// audioFinding words a finding of the audio analysis, linking its spans
// to the track's charts; ok is false for another finding.
func audioFinding(
	f findings.Finding,
	r *analysis.Report,
) (finding, bool) {
	if r.Audio == nil || f.Index < 0 || f.Index >= len(r.Audio.Tracks) {
		return finding{}, false
	}

	track := fmt.Sprintf("Audio #%d", r.Audio.Tracks[f.Index].Stream)
	spans := linkedSpans(f.Spans)

	switch f.Code {
	case findings.AudioSilence:
		return worded(f, spans, "%s: %s", track, plural(len(f.Spans), "silence")), true
	case findings.ChannelSilence:
		return worded(f, spans, "%s: %s silent while the others play (%s)", track, f.Text, plural(len(f.Spans), "segment")), true
	case findings.OutOfPhase:
		return worded(f, spans, "%s: %s out of phase (%s)", track, f.Text, plural(len(f.Spans), "segment")), true
	}

	text := audioLoudnessText(f, r.Audio.Target)
	if text == "" {
		text = audioDefectText(f)
	}

	if text == "" {
		text = audioFormatText(f)
	}

	return worded(f, spans, "%s%s", track, text), text != ""
}

// linkedSpans are the first spans of a finding, as linked on the page.
func linkedSpans(
	list []media.Interval,
) []span {
	out := make([]span, 0, min(len(list), maxSegmentFindings))
	for _, s := range list[:min(len(list), maxSegmentFindings)] {
		out = append(out, spanOf(s))
	}

	return out
}

// audioLoudnessText words the loudness findings.
func audioLoudnessText(
	f findings.Finding,
	target loudness.Target,
) string {
	switch f.Code {
	case findings.LoudnessOffTarget:
		direction := "above"
		if f.Value < f.Limit {
			direction = "below"
		}

		return fmt.Sprintf(": %.1f LUFS, %.1f LU %s the %s target (%g ±%g LUFS)", f.Value, math.Abs(f.Value-f.Limit), direction, f.Text, f.Limit, target.Tolerance)
	case findings.LoudnessOnTarget:
		return fmt.Sprintf(": %.1f LUFS, true peak within %g dBTP: meets the %s target", f.Value, target.MaxTruePeak, f.Text)
	case findings.TruePeakOver:
		return fmt.Sprintf(": true peak %.1f dBTP over the %g dBTP ceiling (%s)", f.Value, f.Limit, plural(len(f.Spans), "passage"))
	case findings.SilentTrack:
		if f.Value <= loudness.Floor {
			return " is silent (digital zero)"
		}

		return fmt.Sprintf(" is silent (peak %.1f dBFS)", f.Value)
	}

	return ""
}

// audioDefectText words the defects holding in one line.
func audioDefectText(
	f findings.Finding,
) string {
	switch f.Code {
	case findings.LeadingSilence:
		return fmt.Sprintf(" starts with %.1f s of silence", f.Value)
	case findings.TrailingSilence:
		return fmt.Sprintf(" ends with %.1f s of silence", f.Value)
	case findings.MutedChannel:
		return fmt.Sprintf(": %s is muted (silent while the others play)", f.Text)
	case findings.EmptyLFE:
		return ": the LFE channel is empty"
	case findings.EmptyCentre:
		return ": the centre channel is empty, where dialogue usually is"
	case findings.Clipping:
		return fmt.Sprintf(": %.0f clipped samples (%s), %s", f.Value, f.Text, plural(len(f.Spans), "segment"))
	case findings.DCOffset:
		return fmt.Sprintf(": %s has a DC offset of %.2f%% of full scale (> %.2f%%)", f.Text, f.Value*100, f.Limit*100)
	case findings.InvertedPolarity:
		return fmt.Sprintf(": %s in opposite polarity (correlation %.2f): one channel is inverted, the mix cancels in mono", f.Text, f.Value)
	case findings.MonoAsStereo:
		return fmt.Sprintf(": %s carry the same signal (mono as stereo)", f.Text)
	}

	return ""
}

// audioFormatText words the findings on the stream's format.
func audioFormatText(
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
		direction := "after"
		if f.Value < 0 {
			direction = "before"
		}

		return fmt.Sprintf(" starts %.3f s %s the video (stream start times, not a lip-sync measurement)", math.Abs(f.Value), direction)
	}

	return ""
}
