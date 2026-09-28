// Package defect finds the technical defects of an audio signal: silence
// (the whole mix, a channel, at the start and the end), muted channels,
// clipping, DC offset, and phase problems between the channels of a pair
// (out-of-phase segments, inverted polarity, two identical channels).
//
// A Detector consumes planar float32 samples (full scale ±1) in chunks of
// any size, in one pass without allocating per sample, and keeps per
// 100 ms step the level of each channel and the correlation of each pair
// for charts. Times are relative to the first sample.
package defect

import (
	"errors"
	"fmt"

	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/media"
)

// Defaults of the options.
const (
	// defaultSilenceThreshold (dBFS) is the level under which a signal is
	// taken for silence: -60 dBFS, the usual threshold of silence
	// detectors (ffmpeg's silencedetect takes -60 dB).
	defaultSilenceThreshold = -60.0
	// defaultSilenceDuration is the shortest silence reported: 2 s, well
	// beyond pauses in speech and music.
	defaultSilenceDuration = 2.0
	// defaultClipLevel (dBFS) is the level from which a sample counts as
	// full scale: -0.01 dBFS, which a 16-bit full-scale sample (32767)
	// reaches.
	defaultClipLevel = -0.01
	// defaultClipRun is the shortest run of full-scale samples counted as
	// clipping: 3 consecutive samples, which a band-limited signal peaking
	// at full scale does not produce.
	defaultClipRun = 3
	// defaultPhaseThreshold is the correlation at or below which a pair
	// is out of phase: -0.5, a clearly negative correlation, where normal
	// stereo stays above 0.
	defaultPhaseThreshold = -0.5
	// defaultPhaseDuration is the shortest out-of-phase segment reported.
	defaultPhaseDuration = 1.0
)

// Options tunes the detector. The zero value uses the defaults.
type Options struct {
	// SilenceThreshold (dBFS): a 10 ms window whose samples all stay
	// below it is silent. Default -60.
	SilenceThreshold float64
	// SilenceDuration is the shortest silence reported. Default 2 s.
	SilenceDuration media.Duration
	// ClipLevel (dBFS) is where full scale starts. Default -0.01.
	ClipLevel float64
	// ClipRun is the least consecutive full-scale samples counted as
	// clipping. Default 3.
	ClipRun int
	// PhaseThreshold is the correlation (over 400 ms) at or below which a
	// pair is out of phase. Default -0.5.
	PhaseThreshold float64
	// PhaseDuration is the shortest out-of-phase segment reported.
	// Default 1 s.
	PhaseDuration media.Duration
}

// WithDefaults fills the unset options with their defaults.
func (o Options) WithDefaults() Options {
	if o.SilenceThreshold == 0 {
		o.SilenceThreshold = defaultSilenceThreshold
	}

	if o.SilenceDuration <= 0 {
		o.SilenceDuration = media.Seconds(defaultSilenceDuration)
	}

	if o.ClipLevel == 0 {
		o.ClipLevel = defaultClipLevel
	}

	if o.ClipRun <= 0 {
		o.ClipRun = defaultClipRun
	}

	if o.PhaseThreshold == 0 {
		o.PhaseThreshold = defaultPhaseThreshold
	}

	if o.PhaseDuration <= 0 {
		o.PhaseDuration = media.Seconds(defaultPhaseDuration)
	}

	return o
}

// windowsPerStep splits a 100 ms step into the 10 ms windows of the
// silence detection.
const windowsPerStep = 10

// ErrChannels is returned by Add for a chunk with another channel count
// than the detector's.
var ErrChannels = errors.New("defect: channel count mismatch")

// Detector finds the defects of a signal. It is not safe for concurrent
// use.
type Detector struct {
	opts Options
	rate int
	// silence is the linear threshold of silence.
	silence  float32
	clip     clipRule
	channels []channel
	pairs    []pair
	// stepSize is the samples in a step (loudness.StepSamples), stepPos
	// the samples of the current step so far, window the index of the
	// current window in the step and pos the samples consumed.
	stepSize, stepPos, window int
	pos                       int64
	// windowStart is the position of the first sample of the window,
	// windowCount the windows closed.
	windowStart, windowCount int64
	mix                      mixState
}

// New returns a detector for channels channels at rate (Hz), measuring
// the correlation of the given pairs of channel indices (the left and
// right of a stereo pair).
func New(
	rate, channels int,
	pairs [][2]int,
	opts Options,
) *Detector {
	opts = opts.WithDefaults()

	d := &Detector{
		opts:    opts,
		rate:    rate,
		silence: float32(amplitude(opts.SilenceThreshold)),
		clip: clipRule{
			level: float32(amplitude(opts.ClipLevel)),
			run:   opts.ClipRun,
			merge: int64(clipMerge * float64(rate)),
		},
		channels: make([]channel, channels),
		stepSize: loudness.StepSamples(rate),
	}

	for _, p := range pairs {
		d.pairs = append(d.pairs, pair{left: p[0], right: p[1]})
	}

	return d
}

// Add consumes the next samples: one slice per channel, all of the same
// length.
func (d *Detector) Add(
	samples [][]float32,
) error {
	if len(samples) != len(d.channels) {
		return fmt.Errorf("%w: %d channels, want %d", ErrChannels, len(samples), len(d.channels))
	}

	if len(samples) == 0 {
		return nil
	}

	frames := len(samples[0])

	for from := 0; from < frames; {
		n := min(frames-from, d.windowEnd()-d.stepPos)
		d.measure(samples, from, from+n)
		from += n
		d.pos += int64(n)

		if d.stepPos += n; d.stepPos == d.windowEnd() {
			d.closeWindow()
		}
	}

	return nil
}

// windowEnd is the position in the step where the current window ends:
// windows split the step evenly, the remainder going to the last ones.
func (d *Detector) windowEnd() int {
	return (d.window + 1) * d.stepSize / windowsPerStep
}

// measure adds the samples [from, to) to the current window.
func (d *Detector) measure(
	samples [][]float32,
	from, to int,
) {
	for c := range d.channels {
		d.channels[c].measure(samples[c][from:to], d.pos, d.clip)
	}

	for i := range d.pairs {
		p := &d.pairs[i]
		p.measure(samples[p.left][from:to], samples[p.right][from:to])
	}
}

// closeWindow ends the current 10 ms window, and the step with its last
// window.
func (d *Detector) closeWindow() {
	d.closeSilenceWindow()

	if d.window++; d.window < windowsPerStep {
		return
	}

	// The pairs read the step's energies before the channels reset them.
	for i := range d.pairs {
		d.pairs[i].closeStep(d.channels, d.phase())
	}

	for c := range d.channels {
		d.channels[c].closeStep(d.stepSize)
	}

	d.window, d.stepPos = 0, 0
}

// seconds converts a sample position into a time from the first sample.
func (d *Detector) seconds(
	sample int64,
) media.Duration {
	return media.Seconds(float64(sample) / float64(d.rate))
}
