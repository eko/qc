package loudness

// The true-peak meter of ITU-R BS.1770 Annex 2: the signal is oversampled
// four times by a 48-tap polyphase FIR interpolator (12 taps per phase,
// the coefficients of the Annex) and the peak of the absolute value of the
// oversampled signal is taken. Four times is the Annex's factor at 48 kHz;
// at 44.1 kHz it reaches 176.4 kHz, and the coefficients, relative to the
// input's Nyquist frequency, hold at any rate. From 192 kHz on, the samples
// themselves are the true peak.

// phaseTaps is the length of each phase of the interpolator.
const phaseTaps = 12

// history is how many past samples an interpolated value needs.
const history = phaseTaps - 1

// oversampledRate is the rate from which a signal is not oversampled: its
// samples are already as dense as the Annex's 4 × 48 kHz.
const oversampledRate = 192000

// interpolator holds the first two phases of the interpolator of
// BS.1770-4 Annex 2, table 1 (every fourth of its 48 coefficients): phase
// p interpolates the point p/4 of a sample period after the centre tap.
// The first phase is almost the identity; the last two phases are the
// first two reversed, the filter being symmetric.
var interpolator = [2][phaseTaps]float64{
	{
		0.0017089843750, 0.0109863281250, -0.0196533203125, 0.0332031250000,
		-0.0594482421875, 0.1373291015625, 0.9721679687500, -0.1022949218750,
		0.0476074218750, -0.0266113281250, 0.0148925781250, -0.0083007812500,
	},
	{
		-0.0291748046875, 0.0292968750000, -0.0517578125000, 0.0891113281250,
		-0.1665039062500, 0.4650878906250, 0.7797851562500, -0.2003173828125,
		0.1015625000000, -0.0582275390625, 0.0330810546875, -0.0189208984375,
	},
}

// peakMeter measures the true peak of one channel.
type peakMeter struct {
	oversample bool
	// buf holds the last history samples, then the samples being measured:
	// every interpolated value reads a contiguous window of it.
	buf []float32
}

func newPeakMeter(
	rate int,
) peakMeter {
	return peakMeter{oversample: rate < oversampledRate, buf: make([]float32, history)}
}

// peak returns the true peak (linear, absolute) of samples, which follow
// the samples of the previous calls.
func (p *peakMeter) peak(
	samples []float32,
) float64 {
	if !p.oversample {
		return samplePeak(samples)
	}

	p.buf = append(p.buf[:history], samples...)
	top := interpolatedPeak(p.buf)
	copy(p.buf, p.buf[len(p.buf)-history:])

	return top
}

// flush returns the true peak around the last samples: their interpolated
// values need the samples that follow, silence past the end.
func (p *peakMeter) flush() float64 {
	if !p.oversample {
		return 0
	}

	var tail [history]float32

	return p.peak(tail[:])
}

// interpolatedPeak is the largest absolute value of the four phases
// interpolated at each sample of buf past its first history samples. The
// taps are unrolled: without a loop over them, the compiler keeps the
// window in registers and checks no bounds, a third faster.
func interpolatedPeak(
	buf []float32,
) float64 {
	h0, h1 := &interpolator[0], &interpolator[1]
	top := 0.0

	for i := 0; i+phaseTaps <= len(buf); i++ {
		w := buf[i : i+phaseTaps : i+phaseTaps]
		// x0 is the newest sample, x11 the oldest.
		x0, x1, x2, x3 := float64(w[11]), float64(w[10]), float64(w[9]), float64(w[8])
		x4, x5, x6, x7 := float64(w[7]), float64(w[6]), float64(w[5]), float64(w[4])
		x8, x9, x10, x11 := float64(w[3]), float64(w[2]), float64(w[1]), float64(w[0])

		y0 := h0[0]*x0 + h0[1]*x1 + h0[2]*x2 + h0[3]*x3 + h0[4]*x4 + h0[5]*x5 +
			h0[6]*x6 + h0[7]*x7 + h0[8]*x8 + h0[9]*x9 + h0[10]*x10 + h0[11]*x11
		y1 := h1[0]*x0 + h1[1]*x1 + h1[2]*x2 + h1[3]*x3 + h1[4]*x4 + h1[5]*x5 +
			h1[6]*x6 + h1[7]*x7 + h1[8]*x8 + h1[9]*x9 + h1[10]*x10 + h1[11]*x11
		y2 := h1[11]*x0 + h1[10]*x1 + h1[9]*x2 + h1[8]*x3 + h1[7]*x4 + h1[6]*x5 +
			h1[5]*x6 + h1[4]*x7 + h1[3]*x8 + h1[2]*x9 + h1[1]*x10 + h1[0]*x11
		y3 := h0[11]*x0 + h0[10]*x1 + h0[9]*x2 + h0[8]*x3 + h0[7]*x4 + h0[6]*x5 +
			h0[5]*x6 + h0[4]*x7 + h0[3]*x8 + h0[2]*x9 + h0[1]*x10 + h0[0]*x11

		top = max(top, abs(y0), abs(y1), abs(y2), abs(y3))
	}

	return top
}

// samplePeak is the largest absolute sample.
func samplePeak(
	samples []float32,
) float64 {
	var top float32
	for _, x := range samples {
		top = max(top, x, -x)
	}

	return float64(top)
}

func abs(
	v float64,
) float64 {
	if v < 0 {
		return -v
	}

	return v
}
