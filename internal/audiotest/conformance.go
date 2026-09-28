package audiotest

import (
	"fmt"
	"math"
	"strings"

	"github.com/eko/qc/audio/loudness"
)

// Measure names a reading of a loudness meter.
type Measure string

// Readings checked by the conformance cases.
const (
	// Integrated is the integrated loudness (LUFS).
	Integrated Measure = "I"
	// Range is the loudness range (LU).
	Range Measure = "LRA"
	// TruePeak is the true peak (dBTP).
	TruePeak Measure = "TP"
	// Momentary and ShortTerm are the momentary and short-term loudness
	// of every complete window, which the case expects constant: the
	// reading is the one furthest from the expected value.
	Momentary Measure = "M"
	ShortTerm Measure = "S"
)

// Check is an expected reading: Want, within [Want-Below, Want+Above].
type Check struct {
	Measure      Measure
	Want         float64
	Below, Above float64
}

// Reading is the value of the check's measure in r.
func (c Check) Reading(
	r loudness.Result,
) float64 {
	switch c.Measure {
	case Integrated:
		return r.Integrated
	case Range:
		return r.Range
	case TruePeak:
		return r.TruePeak
	case Momentary:
		return furthest(r.Series.Momentary[momentaryWarmUp:], c.Want)
	case ShortTerm:
		return furthest(r.Series.ShortTerm[shortTermWarmUp:], c.Want)
	}

	return math.NaN()
}

// Warm-up of the momentary and short-term series: the windows of their
// first values reach before the start.
const (
	momentaryWarmUp = 3
	shortTermWarmUp = 29
)

// Pass reports whether reading is within the check's tolerance (with a
// slack for the 0.01 rounding of the readings).
func (c Check) Pass(
	reading float64,
) bool {
	const slack = 1e-9

	return reading >= c.Want-c.Below-slack && reading <= c.Want+c.Above+slack
}

// Tolerance words the tolerance: ±0.1 or +0.2/−0.4.
func (c Check) Tolerance() string {
	if c.Below == c.Above {
		return fmt.Sprintf("±%g", c.Above)
	}

	return fmt.Sprintf("+%g/−%g", c.Above, c.Below)
}

// furthest is the value of values furthest from want.
func furthest(
	values []float64,
	want float64,
) float64 {
	worst := want
	for _, v := range values {
		if math.Abs(v-want) > math.Abs(worst-want) {
			worst = v
		}
	}

	return worst
}

// Case is a conformance signal and the readings the specification expects.
type Case struct {
	// Name is the specification and case number ("3341-1").
	Name string
	// Description is what the signal is.
	Description string
	// Weights are the BS.1770 weights of its channels.
	Weights []float64
	// Signal synthesises its channels at Rate.
	Signal func() [][]float32
	Checks []Check
}

// Tolerances of the specifications.
const (
	// meterTolerance is the ±0.1 LU of the momentary, short-term and
	// integrated readings (Tech 3341).
	meterTolerance = 0.1
	// rangeTolerance is the ±1 LU of the loudness range (Tech 3342).
	rangeTolerance = 1
	// truePeakAbove and truePeakBelow are the +0.2/−0.4 dB of the true
	// peak (Tech 3341).
	truePeakAbove = 0.2
	truePeakBelow = 0.4
)

// Channel weights of the stereo, mono and 5.0 signals (L, R, C, Ls, Rs).
var (
	stereo   = []float64{1, 1}
	mono     = []float64{1}
	surround = []float64{1, 1, 1, 1.41, 1.41}
)

// meter is a ±0.1 check of a loudness reading.
func meter(
	m Measure,
	want float64,
) Check {
	return Check{Measure: m, Want: want, Below: meterTolerance, Above: meterTolerance}
}

// stereoTones is a stereo 1 kHz signal of tones.
func stereoTones(
	tones ...Tone,
) func() [][]float32 {
	return func() [][]float32 { return Channels(2, Sine(Rate, toneFrequency, 0, tones...)) }
}

// Tech3341 returns the cases of EBU Tech 3341 (v4, 2023) that the
// specification describes as synthetic signals: 1 to 6, 9 and 12
// (loudness), 15 to 19 (true peak). Cases 7, 8, 10, 11, 13 and 14 are
// programme excerpts or sets of files, and 20 to 23 recorded signals: they
// cannot be synthesised from the text.
func Tech3341() []Case {
	return append(tech3341Loudness(), tech3341TruePeak()...)
}

func tech3341Loudness() []Case {
	return []Case{
		{
			Name: "3341-1", Description: "stereo 1 kHz, -23 dBFS, 20 s", Weights: stereo,
			Signal: stereoTones(Tone{-23, 20}),
			Checks: []Check{meter(Momentary, -23), meter(ShortTerm, -23), meter(Integrated, -23)},
		},
		{
			Name: "3341-2", Description: "stereo 1 kHz, -33 dBFS, 20 s", Weights: stereo,
			Signal: stereoTones(Tone{-33, 20}),
			Checks: []Check{meter(Momentary, -33), meter(ShortTerm, -33), meter(Integrated, -33)},
		},
		{
			Name: "3341-3", Description: "stereo 1 kHz, -36 / -23 / -36 dBFS, 10 / 60 / 10 s", Weights: stereo,
			Signal: stereoTones(Tone{-36, 10}, Tone{-23, 60}, Tone{-36, 10}),
			Checks: []Check{meter(Integrated, -23)},
		},
		{
			Name: "3341-4", Description: "stereo 1 kHz, -72 / -36 / -23 / -36 / -72 dBFS, 10 / 10 / 60 / 10 / 10 s", Weights: stereo,
			Signal: stereoTones(Tone{-72, 10}, Tone{-36, 10}, Tone{-23, 60}, Tone{-36, 10}, Tone{-72, 10}),
			Checks: []Check{meter(Integrated, -23)},
		},
		{
			Name: "3341-5", Description: "stereo 1 kHz, -26 / -20 / -26 dBFS, 20 / 20.1 / 20 s", Weights: stereo,
			Signal: stereoTones(Tone{-26, 20}, Tone{-20, 20.1}, Tone{-26, 20}),
			Checks: []Check{meter(Integrated, -23)},
		},
		{
			Name: "3341-6", Description: "5.0 1 kHz: L, R -28, C -24, Ls, Rs -30 dBFS, 20 s", Weights: surround,
			Signal: func() [][]float32 {
				side := Sine(Rate, toneFrequency, 0, Tone{-28, 20})
				return [][]float32{side, side, Sine(Rate, toneFrequency, 0, Tone{-24, 20}),
					Sine(Rate, toneFrequency, 0, Tone{-30, 20}), Sine(Rate, toneFrequency, 0, Tone{-30, 20})}
			},
			Checks: []Check{meter(Integrated, -23)},
		},
		{
			Name: "3341-9", Description: "stereo 1 kHz, 1.34 s at -20 / 1.66 s at -30 dBFS, 5 times", Weights: stereo,
			Signal: stereoTones(Repeat(5, Tone{-20, 1.34}, Tone{-30, 1.66})...),
			Checks: []Check{meter(ShortTerm, -23)},
		},
		{
			Name: "3341-12", Description: "stereo 1 kHz, 0.18 s at -20 / 0.22 s at -30 dBFS, 25 times", Weights: stereo,
			Signal: stereoTones(Repeat(25, Tone{-20, 0.18}, Tone{-30, 0.22})...),
			Checks: []Check{meter(Momentary, -23)},
		},
	}
}

// truePeakFade is the ramp at both ends of the true-peak signals: the
// abrupt onset of a sine at a non-zero phase rings in the interpolator,
// up to 0.7 dB above the waveform's peak at fs/8 (the Gibbs overshoot of a
// step, which a signal described by its steady state does not have).
const truePeakFade = 0.02

// truePeakCase is a mono sine at a fraction of the sample rate and a
// starting phase whose true peak is level: its samples fall between the
// peaks of the waveform.
func truePeakCase(
	name string,
	fraction, phase, level float64,
) Case {
	return Case{
		Name:        name,
		Description: fmt.Sprintf("mono sine at fs/%g, %g° phase, %+g dBTP", fraction, phase, level),
		Weights:     mono,
		Signal: func() [][]float32 {
			return [][]float32{Fade(Sine(Rate, Rate/fraction, phase, Tone{level, 1}), Rate, truePeakFade)}
		},
		Checks: []Check{{Measure: TruePeak, Want: level, Below: truePeakBelow, Above: truePeakAbove}},
	}
}

func tech3341TruePeak() []Case {
	return []Case{
		truePeakCase("3341-15", 4, 0, -6),
		truePeakCase("3341-16", 4, 45, -6),
		truePeakCase("3341-17", 6, 60, -6),
		truePeakCase("3341-18", 8, 67.5, -6),
		truePeakCase("3341-19", 4, 45, 3),
	}
}

// Tech3342 returns the cases of EBU Tech 3342 (v4, 2023) that are
// synthetic signals: 1 to 4. Cases 5 and 6 are programme excerpts.
func Tech3342() []Case {
	lra := func(name string, want float64, tones ...Tone) Case {
		return Case{
			Name:        name,
			Description: toneList(tones),
			Weights:     stereo,
			Signal:      stereoTones(tones...),
			Checks:      []Check{{Measure: Range, Want: want, Below: rangeTolerance, Above: rangeTolerance}},
		}
	}

	return []Case{
		lra("3342-1", 10, Tone{-20, 20}, Tone{-30, 20}),
		lra("3342-2", 5, Tone{-20, 20}, Tone{-15, 20}),
		lra("3342-3", 20, Tone{-40, 20}, Tone{-20, 20}),
		lra("3342-4", 15, Tone{-50, 20}, Tone{-35, 20}, Tone{-20, 20}, Tone{-35, 20}, Tone{-50, 20}),
	}
}

// toneList describes a stereo tone sequence.
func toneList(
	tones []Tone,
) string {
	parts := make([]string, len(tones))
	for i, t := range tones {
		parts[i] = fmt.Sprintf("%g s at %g dBFS", t.Seconds, t.Level)
	}

	return "stereo 1 kHz, " + strings.Join(parts, " then ")
}
