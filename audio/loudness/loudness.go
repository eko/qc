// Package loudness measures programme loudness as ITU-R BS.1770-5 defines
// it, with the meters of EBU Tech 3341 (momentary, short-term, integrated)
// and the loudness range of EBU Tech 3342, as EBU R 128 and ATSC A/85
// use them, and checks it against a delivery target.
//
// A Meter consumes planar float32 samples (full scale ±1) in chunks of any
// size and keeps one weighted energy per 100 ms of signal: every
// measurement (the 400 ms momentary and 3 s short-term windows, the gated
// integrated loudness, the loudness range) is computed from those energies
// at the end, exactly as the standards define it. The true peak is
// measured on the fly by 4× oversampling (BS.1770 Annex 2).
//
// The package is pure Go, allocation-free per sample, and knows nothing of
// files or decoders: see package audio for a track's whole analysis.
package loudness

import (
	"errors"
	"fmt"
	"math"
)

// Floor stands for silence (the logarithm of zero energy) in results and
// series: JSON has no infinity.
const Floor = -120.0

// Step is the resolution of the meter and of its series, in seconds: the
// 100 ms hop of the gating blocks of BS.1770 and of the short-term meter of
// EBU Tech 3342 (at least 10 Hz).
const Step = 0.1

// Windows of the meters, in steps: 400 ms momentary (also the gating
// block of the integrated loudness), 3 s short-term.
const (
	momentarySteps = 4
	shortTermSteps = 30
)

// Gates of BS.1770 (integrated loudness) and EBU Tech 3342 (loudness
// range), in LUFS and LU.
const (
	absoluteGate      = -70.0
	relativeGate      = -10.0
	rangeRelativeGate = -20.0
)

// Percentiles of the short-term distribution bounding the loudness range
// (EBU Tech 3342).
const (
	rangeLow  = 0.10
	rangeHigh = 0.95
)

// offset is the -0.691 dB of the loudness formula of BS.1770: it makes a
// 1 kHz sine at -23 dBFS peak on both channels of a stereo pair read
// -23 LUFS.
const offset = -0.691

// stepsPerSecond converts a rate into samples per step.
const stepsPerSecond = 10

// ErrChannels is returned by Add for a chunk with another channel count
// than the meter's.
var ErrChannels = errors.New("loudness: channel count mismatch")

// Meter measures the loudness and the true peak of a signal. It is not
// safe for concurrent use.
type Meter struct {
	weights []float64
	filters []kFilter
	peaks   []peakMeter
	// stepSize is the number of samples in a step, pos the samples of the
	// current step consumed so far, energy the weighted energy of the
	// current step and peak its true peak (linear).
	stepSize int
	pos      int
	energy   float64
	peak     float64
	// energies and stepPeaks are the weighted energy (sum of squares, not
	// yet averaged) and the true peak of every complete step.
	energies  []float64
	stepPeaks []float64
	// channelPeaks is the true peak of each channel, samplePeak the largest
	// sample of any channel.
	channelPeaks []float64
	samplePeak   float64
}

// NewMeter returns a meter for a signal at rate (Hz) whose channels have
// the given weights (BS.1770: 1 for the front channels, 1.41 for the
// surround ones, 0 for the LFE, which only counts in the true peak).
func NewMeter(
	rate int,
	weights []float64,
) *Meter {
	m := &Meter{
		weights:      weights,
		filters:      make([]kFilter, len(weights)),
		peaks:        make([]peakMeter, len(weights)),
		stepSize:     StepSamples(rate),
		channelPeaks: make([]float64, len(weights)),
	}

	for c := range weights {
		m.filters[c] = newKFilter(rate)
		m.peaks[c] = newPeakMeter(rate)
	}

	return m
}

// StepSamples is the number of samples in a step (100 ms) at rate,
// rounded: 4800 at 48 kHz, 4410 at 44.1 kHz.
func StepSamples(
	rate int,
) int {
	return max(1, (rate+stepsPerSecond/2)/stepsPerSecond)
}

// Add measures the next samples: one slice per channel, all of the same
// length.
func (m *Meter) Add(
	samples [][]float32,
) error {
	if len(samples) != len(m.weights) {
		return fmt.Errorf("%w: %d channels, want %d", ErrChannels, len(samples), len(m.weights))
	}

	if len(samples) == 0 {
		return nil
	}

	frames := len(samples[0])

	for from := 0; from < frames; {
		n := min(frames-from, m.stepSize-m.pos)
		m.measure(samples, from, from+n)
		from += n

		if m.pos += n; m.pos == m.stepSize {
			m.closeStep()
		}
	}

	return nil
}

// measure adds the samples [from, to) of every channel to the current
// step.
func (m *Meter) measure(
	samples [][]float32,
	from, to int,
) {
	for c, channel := range samples {
		chunk := channel[from:to]

		if w := m.weights[c]; w != 0 {
			m.energy += w * m.filters[c].energy(chunk)
		}

		// The interpolator attenuates a lone sample (its centre tap is
		// 0.97): the true peak is never below the sample peak, as in
		// libebur128.
		sample := samplePeak(chunk)
		m.samplePeak = max(m.samplePeak, sample)
		peak := max(sample, m.peaks[c].peak(chunk))
		m.channelPeaks[c] = max(m.channelPeaks[c], peak)
		m.peak = max(m.peak, peak)
	}
}

// closeStep records the current step.
func (m *Meter) closeStep() {
	m.energies = append(m.energies, m.energy)
	m.stepPeaks = append(m.stepPeaks, m.peak)
	m.energy, m.peak, m.pos = 0, 0, 0
}

// Result computes the measurements. It flushes the true-peak meters: the
// meter must not be fed afterwards. A last step shorter than 100 ms is
// left out of the loudness measurements, as an incomplete gating block is
// (its samples count in the true peak).
func (m *Meter) Result() Result {
	for c := range m.peaks {
		tail := m.peaks[c].flush()
		m.channelPeaks[c] = max(m.channelPeaks[c], tail)

		// The tail belongs to the last samples: the pending step's, or the
		// last complete one's.
		if n := len(m.stepPeaks); m.pos == 0 && n > 0 {
			m.stepPeaks[n-1] = max(m.stepPeaks[n-1], tail)
		} else {
			m.peak = max(m.peak, tail)
		}
	}

	norm := func(steps int) float64 { return 1 / float64(steps*m.stepSize) }
	momentary := windowed(m.energies, momentarySteps, norm(momentarySteps))
	shortTerm := windowed(m.energies, shortTermSteps, norm(shortTermSteps))

	r := Result{
		Integrated:   Round(integrated(complete(momentary, momentarySteps))),
		SamplePeak:   Round(Decibels(m.samplePeak)),
		MaxMomentary: maxLevel(complete(momentary, momentarySteps)),
		MaxShortTerm: maxLevel(complete(shortTerm, shortTermSteps)),
		Series: Series{
			Momentary: rounded(momentary),
			ShortTerm: rounded(shortTerm),
			TruePeak:  roundedDecibels(m.stepPeaks),
		},
	}

	r.Range, r.RangeLow, r.RangeHigh = loudnessRange(complete(shortTerm, shortTermSteps))
	r.TruePeak, r.TruePeakAt = m.truePeak()

	r.ChannelTruePeaks = make([]float64, len(m.channelPeaks))
	for c, p := range m.channelPeaks {
		r.ChannelTruePeaks[c] = Round(Decibels(p))
	}

	return r
}

// truePeak is the true peak of every channel, in dBTP, and the start of
// the step holding it, in seconds from the first sample.
func (m *Meter) truePeak() (float64, float64) {
	top := 0.0
	for _, p := range m.channelPeaks {
		top = max(top, p)
	}

	at, best := 0, -1.0
	for i, p := range m.stepPeaks {
		if p > best {
			at, best = i, p
		}
	}

	// The pending step (shorter than 100 ms) may hold it.
	if m.pos > 0 && m.peak > best {
		at = len(m.stepPeaks)
	}

	return Round(Decibels(top)), float64(at) * Step
}

// Decibels converts a linear amplitude to dB (dBFS for a sample value),
// Floor for silence.
func Decibels(
	amplitude float64,
) float64 {
	if amplitude <= 0 {
		return Floor
	}

	return max(Floor, 20*math.Log10(amplitude))
}

// level converts a weighted mean square to LUFS, Floor for silence.
func level(
	meanSquare float64,
) float64 {
	if meanSquare <= 0 {
		return Floor
	}

	return max(Floor, offset+10*math.Log10(meanSquare))
}

// seriesDecimals is the precision of series values (0.01 LU): finer digits
// are noise and only bloat reports.
const seriesDecimals = 100

// Round rounds v to the precision of the results (0.01).
func Round(
	v float64,
) float64 {
	return math.Round(v*seriesDecimals) / seriesDecimals
}

func rounded(
	values []float64,
) []float64 {
	out := make([]float64, len(values))
	for i, v := range values {
		out[i] = Round(v)
	}

	return out
}

func roundedDecibels(
	amplitudes []float64,
) []float64 {
	out := make([]float64, len(amplitudes))
	for i, a := range amplitudes {
		out[i] = Round(Decibels(a))
	}

	return out
}
