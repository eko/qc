package loudness

import "math"

// The K-weighting of ITU-R BS.1770 is a high shelf (+4 dB above ~1.5 kHz,
// the acoustic effect of the head) followed by a high-pass (the RLB curve).
// The standard gives its coefficients at 48 kHz only; these are the
// parameters of the analogue prototypes behind them (as derived by
// libebur128), so that the bilinear transform gives the standard's
// coefficients at 48 kHz, to 1e-12, and the same response at any other
// rate.
const (
	shelfFrequency = 1681.974450955533
	shelfGainDB    = 3.999843853973347
	shelfQ         = 0.7071752369554196
	// shelfBandExponent sets the shelf's band gain from its high gain.
	shelfBandExponent = 0.4996667741545416
	highPassFrequency = 38.13547087602444
	highPassQ         = 0.5003270373238773
)

// biquad holds the normalised coefficients of a second-order section
// (a0 = 1).
type biquad struct {
	b0, b1, b2, a1, a2 float64
}

// kWeighting returns the shelf and high-pass stages at rate.
func kWeighting(
	rate int,
) (shelf, highPass biquad) {
	k := math.Tan(math.Pi * shelfFrequency / float64(rate))
	vh := math.Pow(10, shelfGainDB/20)
	vb := math.Pow(vh, shelfBandExponent)
	a0 := 1 + k/shelfQ + k*k

	shelf = biquad{
		b0: (vh + vb*k/shelfQ + k*k) / a0,
		b1: 2 * (k*k - vh) / a0,
		b2: (vh - vb*k/shelfQ + k*k) / a0,
		a1: 2 * (k*k - 1) / a0,
		a2: (1 - k/shelfQ + k*k) / a0,
	}

	k = math.Tan(math.Pi * highPassFrequency / float64(rate))
	a0 = 1 + k/highPassQ + k*k

	highPass = biquad{
		b0: 1,
		b1: -2,
		b2: 1,
		a1: 2 * (k*k - 1) / a0,
		a2: (1 - k/highPassQ + k*k) / a0,
	}

	return shelf, highPass
}

// kFilter is the K-weighting filter of one channel: both stages in
// transposed direct form II, in float64 (the high-pass pole sits very close
// to 1 at 48 kHz, where float32 state would drift).
type kFilter struct {
	shelf, highPass biquad
	// s1, s2 and h1, h2 are the states of the shelf and the high-pass.
	s1, s2, h1, h2 float64
}

func newKFilter(
	rate int,
) kFilter {
	shelf, highPass := kWeighting(rate)

	return kFilter{shelf: shelf, highPass: highPass}
}

// energy filters samples and returns the sum of the squared output.
func (f *kFilter) energy(
	samples []float32,
) float64 {
	s, h := f.shelf, f.highPass
	s1, s2, h1, h2 := f.s1, f.s2, f.h1, f.h2
	sum := 0.0

	for _, x := range samples {
		in := float64(x)
		y := s.b0*in + s1
		s1 = s.b1*in - s.a1*y + s2
		s2 = s.b2*in - s.a2*y

		z := y + h1
		h1 = -2*y - h.a1*z + h2
		h2 = y - h.a2*z

		sum += z * z
	}

	f.s1, f.s2, f.h1, f.h2 = s1, s2, h1, h2

	return sum
}
