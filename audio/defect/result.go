package defect

import (
	"math"

	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/media"
)

// mutedShare is the share of the time the mix plays during which a
// channel must stay silent to count as muted: 99%, so that a click or a
// burst of noise on a dead channel does not hide it.
const mutedShare = 0.99

// identicalBelow is the level (dB) of the difference of two channels
// relative to their mean energy under which they carry the same signal:
// -35 dB, a correlation above 0.9998, which no stereo mix keeps over a
// programme (a narrow one reads -10 to -25 dB), while a lossy codec coding
// the same signal twice leaves a difference around -45 dB (AAC-LC at
// 256 kb/s: -47 dB).
const identicalBelow = -35.0

// Result lists the defects of a signal.
type Result struct {
	// Duration is the length of the signal.
	Duration media.Duration `json:"duration"`
	// Silence are the silences of the whole mix lasting at least the
	// minimum duration, leading and trailing ones included.
	Silence []media.Interval `json:"silence,omitempty"`
	// LeadingSilence and TrailingSilence are the silence at the start and
	// the end, whatever their length.
	LeadingSilence  media.Duration `json:"leadingSilence"`
	TrailingSilence media.Duration `json:"trailingSilence"`
	Channels        []Channel      `json:"channels"`
	Pairs           []Pair         `json:"pairs,omitempty"`
	// Levels are the RMS levels (dBFS) of every channel per 100 ms step
	// (loudness.Step), as the loudness series.
	Levels [][]float64 `json:"levels"`
}

// Channel sums one channel up.
type Channel struct {
	// Peak is the sample peak and RMS the RMS level of the whole signal,
	// in dBFS; DC is its mean (full scale ±1).
	Peak float64 `json:"peak"`
	RMS  float64 `json:"rms"`
	DC   float64 `json:"dc"`
	// Silent is the share of the time the channel is silent.
	Silent float64 `json:"silent"`
	// Muted is true when the channel stays silent while the other channels
	// play (a dead or muted channel); false when the whole mix is silent.
	Muted bool `json:"muted,omitempty"`
	// Silence are the silences of this channel alone (the mix playing)
	// lasting at least the minimum duration.
	Silence []media.Interval `json:"silence,omitempty"`
	// ClippedSamples are the samples in runs of full-scale samples,
	// ClipEvents the runs, and Clipping the clipped segments (events
	// merged when 0.5 s apart or less, the first 100).
	ClippedSamples int64            `json:"clippedSamples,omitempty"`
	ClipEvents     int              `json:"clipEvents,omitempty"`
	Clipping       []media.Interval `json:"clipping,omitempty"`
}

// Pair sums a pair of channels up.
type Pair struct {
	// Left and Right are the channel indices.
	Left  int `json:"left"`
	Right int `json:"right"`
	// Correlation is the correlation of the whole signal (+1 same signal,
	// -1 opposite polarities).
	Correlation float64 `json:"correlation"`
	// Difference is the energy of L − R relative to the mean energy of L
	// and R, in dB: loudness.Floor for identical channels, 0 for unrelated
	// ones, +3 for opposite ones.
	Difference float64 `json:"difference"`
	// Identical is true when the channels carry the same signal (a mono
	// signal on two channels).
	Identical bool `json:"identical,omitempty"`
	// Inverted is true when the correlation of the whole signal is at or
	// below the phase threshold: one channel's polarity is inverted.
	Inverted bool `json:"inverted,omitempty"`
	// OutOfPhase are the segments where the correlation stays at or below
	// the threshold.
	OutOfPhase []media.Interval `json:"outOfPhase,omitempty"`
	// Correlation series over 400 ms windows, per 100 ms step (0 while
	// either channel is silent).
	Series []float64 `json:"series"`
}

// Result finishes the detection: the last, shorter window counts for the
// silences, the open runs are closed.
func (d *Detector) Result() Result {
	if d.pos > d.windowStart {
		d.closeSilenceWindow()
	}

	minLength := d.samples(d.opts.SilenceDuration.Seconds())
	r := Result{Duration: d.seconds(d.pos)}

	// The edges read the mix's open run, which mixSilences closes.
	r.LeadingSilence, r.TrailingSilence = d.edges()
	r.Silence = d.mixSilences(minLength)

	for c := range d.channels {
		ch := &d.channels[c]
		if ch.clipRun > 0 {
			ch.endClip(d.clip)
		}

		if s, ok := ch.silence.close(minLength); ok {
			ch.silences = append(ch.silences, s)
		}

		r.Channels = append(r.Channels, d.channelResult(ch))
		r.Levels = append(r.Levels, ch.levels)
	}

	for i := range d.pairs {
		r.Pairs = append(r.Pairs, d.pairResult(&d.pairs[i]))
	}

	return r
}

// mixSilences closes the mix's silence and returns its segments.
func (d *Detector) mixSilences(
	minLength int64,
) []media.Interval {
	var out []media.Interval

	spans := d.mix.silences
	if s, ok := d.mix.run.close(minLength); ok {
		spans = append(spans, s)
	}

	for _, s := range spans {
		out = append(out, d.interval(s))
	}

	return out
}

// edges are the leading and trailing silences. The mix's run must not be
// closed yet.
func (d *Detector) edges() (leading, trailing media.Duration) {
	if !d.mix.started {
		return d.seconds(d.pos), d.seconds(d.pos)
	}

	if d.mix.run.open {
		trailing = d.seconds(d.pos - d.mix.run.from)
	}

	return d.seconds(d.mix.leading), trailing
}

func (d *Detector) channelResult(
	ch *channel,
) Channel {
	out := Channel{
		Peak:           loudness.Round(loudness.Decibels(float64(ch.peak))),
		RMS:            loudness.Round(rmsDecibels(ch.squares, d.pos)),
		ClippedSamples: ch.clippedSamples,
		ClipEvents:     ch.clipEvents,
	}

	if d.pos > 0 {
		out.DC = ch.sum / float64(d.pos)
	}

	if d.windowCount > 0 {
		out.Silent = float64(ch.silentWindows) / float64(d.windowCount)
	}

	if active := d.mix.activeWindows; active > 0 {
		out.Muted = float64(ch.silentActive) >= mutedShare*float64(active)
	}

	for _, s := range ch.silences {
		out.Silence = append(out.Silence, d.interval(s))
	}

	for _, s := range ch.clips {
		out.Clipping = append(out.Clipping, d.interval(s))
	}

	return out
}

func (d *Detector) pairResult(
	p *pair,
) Pair {
	left, right := d.channels[p.left].squares, d.channels[p.right].squares

	out := Pair{
		Left:        p.left,
		Right:       p.right,
		Correlation: loudness.Round(correlation(p.totalCross, left, right)),
		Difference:  loudness.Floor,
		Series:      p.correlation,
	}

	out.Inverted = out.Correlation <= d.opts.PhaseThreshold

	if mean := (left + right) / 2; mean > 0 {
		out.Difference = loudness.Round(energyDecibels(p.totalDiff / mean))
		// Two silent channels are alike, not a mono signal.
		audible := mean/float64(d.pos) > float64(d.silence)*float64(d.silence)
		out.Identical = audible && out.Difference <= identicalBelow
	}

	if s, ok := p.outOfPhase.close(d.phase().minLength); ok {
		p.segments = append(p.segments, s)
	}

	for _, s := range p.segments {
		out.OutOfPhase = append(out.OutOfPhase, d.interval(s))
	}

	return out
}

// interval converts a span of samples into times.
func (d *Detector) interval(
	s span,
) media.Interval {
	return media.Interval{Start: d.seconds(s.from), End: d.seconds(s.to)}
}

// energyDecibels converts an energy ratio to dB, loudness.Floor for zero.
func energyDecibels(
	ratio float64,
) float64 {
	if ratio <= 0 {
		return loudness.Floor
	}

	return max(loudness.Floor, 10*math.Log10(ratio))
}
