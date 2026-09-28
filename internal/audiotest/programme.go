package audiotest

import (
	"math"
	"math/rand/v2"
)

// Programme synthesis: a stereo signal that behaves like programme audio
// for the defect detectors (never silent, never clipped, positively
// correlated channels, a level varying like speech around -23 LUFS), and
// deterministic.
const (
	// syllableRate (Hz) modulates the level like syllables of speech.
	syllableRate = 4
	// envelopeFloor is the lowest level of the modulation (-14 dB).
	envelopeFloor = 0.2
	// programmeGain sets the level: about -23 LUFS, peaks near -8 dBFS.
	programmeGain = 0.16
	// sideShare is the share of each channel's own signal: the channels
	// correlate at about 0.9, like a mix centred on dialogue.
	sideShare = 0.3
	// noiseShare is the share of the pink-ish noise in the signal.
	noiseShare = 0.5
	// noisePole smooths the white noise into a low-passed one.
	noisePole = 0.9
)

// partials are the frequencies (Hz) of the tonal part of the programme.
var partials = []float64{220, 440, 660, 1320, 2640}

// Programme returns a stereo programme-like signal of the given length at
// rate, from seed.
func Programme(
	rate int,
	seconds float64,
	seed uint64,
) [][]float32 {
	rng := rand.New(rand.NewPCG(seed, seed+1)) //nolint:gosec // seeded on purpose: reproducible signals
	n := int(seconds * float64(rate))
	left, right := make([]float32, n), make([]float32, n)

	var common, ownL, ownR float64

	for i := range n {
		t := float64(i) / float64(rate)
		envelope := envelopeFloor + (1-envelopeFloor)*0.5*(1+math.Sin(2*math.Pi*syllableRate*t+math.Sin(0.7*t)))

		tone := 0.0
		for k, f := range partials {
			tone += math.Sin(2*math.Pi*f*t+float64(k)) / float64(k+1)
		}

		common = noisePole*common + (1-noisePole)*rng.NormFloat64()*4
		ownL = noisePole*ownL + (1-noisePole)*rng.NormFloat64()*4
		ownR = noisePole*ownR + (1-noisePole)*rng.NormFloat64()*4

		mid := (1-noiseShare)*tone/2 + noiseShare*common
		left[i] = float32(programmeGain * envelope * ((1-sideShare)*mid + sideShare*ownL))
		right[i] = float32(programmeGain * envelope * ((1-sideShare)*mid + sideShare*ownR))
	}

	return [][]float32{left, right}
}

// span converts [from, to) seconds into sample indices within x.
func span(
	x []float32,
	rate int,
	from, to float64,
) []float32 {
	return x[min(len(x), int(from*float64(rate))):min(len(x), int(to*float64(rate)))]
}

// Mute zeroes x over [from, to) seconds.
func Mute(
	x []float32,
	rate int,
	from, to float64,
) {
	clear(span(x, rate, from, to))
}

// Invert inverts the polarity of x over [from, to) seconds.
func Invert(
	x []float32,
	rate int,
	from, to float64,
) {
	s := span(x, rate, from, to)
	for i := range s {
		s[i] = -s[i]
	}
}

// Clip amplifies x by gain over [from, to) seconds and clips it at full
// scale, as an overloaded converter or a limiter-less mix does.
func Clip(
	x []float32,
	rate int,
	from, to, gain float64,
) {
	s := span(x, rate, from, to)
	for i := range s {
		s[i] = float32(max(-1, min(1, float64(s[i])*gain)))
	}
}

// Offset adds a DC offset to x.
func Offset(
	x []float32,
	dc float64,
) {
	for i := range x {
		x[i] += float32(dc)
	}
}
