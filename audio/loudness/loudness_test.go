package loudness

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKWeighting48k(
	t *testing.T,
) {
	// The coefficients of ITU-R BS.1770-5, tables 1 and 2.
	shelf, highPass := kWeighting(48000)

	testCases := []struct {
		name      string
		got, want float64
	}{
		{name: "shelf b0", got: shelf.b0, want: 1.53512485958697},
		{name: "shelf b1", got: shelf.b1, want: -2.69169618940638},
		{name: "shelf b2", got: shelf.b2, want: 1.19839281085285},
		{name: "shelf a1", got: shelf.a1, want: -1.69065929318241},
		{name: "shelf a2", got: shelf.a2, want: 0.73248077421585},
		{name: "high-pass b0", got: highPass.b0, want: 1},
		{name: "high-pass b1", got: highPass.b1, want: -2},
		{name: "high-pass b2", got: highPass.b2, want: 1},
		{name: "high-pass a1", got: highPass.a1, want: -1.99004745483398},
		{name: "high-pass a2", got: highPass.a2, want: 0.99007225036621},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, testCase.got, 1e-12)
		})
	}
}

// sine is a 1 kHz sine of amplitude a, n samples at rate.
func sine(
	rate, n int,
	a float64,
) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(a * math.Sin(2*math.Pi*1000*float64(i)/float64(rate)))
	}

	return out
}

func TestMeterRates(
	t *testing.T,
) {
	// A 1 kHz sine at -23 dBFS on both channels reads -23 LUFS at any rate
	// (the K-weighting is derived per rate).
	testCases := []struct {
		name string
		rate int
	}{
		{name: "44.1 kHz", rate: 44100},
		{name: "48 kHz", rate: 48000},
		{name: "96 kHz", rate: 96000},
		{name: "192 kHz, no oversampling", rate: 192000},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			x := sine(testCase.rate, 5*testCase.rate, math.Pow(10, -23.0/20))
			m := NewMeter(testCase.rate, []float64{1, 1})
			require.NoError(t, m.Add([][]float32{x, x}))

			r := m.Result()
			assert.InDelta(t, -23, r.Integrated, 0.05)
			assert.InDelta(t, -23, r.TruePeak, 0.3)
			assert.InDelta(t, -23, r.SamplePeak, 0.3)
			assert.Len(t, r.Series.Momentary, 50)
		})
	}
}

func TestMeterChunks(
	t *testing.T,
) {
	// Any chunking gives the same result.
	x := sine(48000, 48000*4+1234, 0.1)
	whole := NewMeter(48000, []float64{1})
	require.NoError(t, whole.Add([][]float32{x}))

	pieces := NewMeter(48000, []float64{1})
	for from := 0; from < len(x); from += 999 {
		require.NoError(t, pieces.Add([][]float32{x[from:min(len(x), from+999)]}))
	}

	assert.Equal(t, whole.Result(), pieces.Result())
}

func TestMeterErrors(
	t *testing.T,
) {
	m := NewMeter(48000, []float64{1, 1})
	require.ErrorIs(t, m.Add([][]float32{{0}}), ErrChannels)

	empty := NewMeter(48000, nil)
	require.NoError(t, empty.Add(nil))
}

func TestMeterSilence(
	t *testing.T,
) {
	m := NewMeter(48000, []float64{1})
	require.NoError(t, m.Add([][]float32{make([]float32, 48000)}))

	r := m.Result()
	assert.Equal(t, Floor, r.Integrated)
	assert.Equal(t, Floor, r.TruePeak)
	assert.Equal(t, Floor, r.MaxShortTerm)
	assert.Zero(t, r.Range)
	assert.Equal(t, Floor, r.RangeLow)
	assert.Equal(t, []float64{Floor}, r.ChannelTruePeaks)
}

func TestTruePeakAt(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		frames int
		click  int
		want   float64
	}{
		{name: "in a complete step", frames: 48000, click: 24000, want: 0.5},
		{name: "in the last, shorter step", frames: 48000 + 100, click: 48000 + 50, want: 1},
		{name: "at the very end", frames: 48000, click: 47999, want: 0.9},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			x := make([]float32, testCase.frames)
			x[testCase.click] = 0.5

			m := NewMeter(48000, []float64{1})
			require.NoError(t, m.Add([][]float32{x}))

			r := m.Result()
			assert.InDelta(t, testCase.want, r.TruePeakAt, 1e-9)
			// The sample peak: the interpolator attenuates a lone sample.
			assert.InDelta(t, -6.02, r.TruePeak, 0.01)
		})
	}
}

func TestLFEWeight(
	t *testing.T,
) {
	// A weight of 0 leaves the channel out of the loudness, not of the
	// true peak.
	loud := sine(48000, 48000*2, 0.5)
	quiet := sine(48000, 48000*2, 0.05)

	m := NewMeter(48000, []float64{1, 0})
	require.NoError(t, m.Add([][]float32{quiet, loud}))

	r := m.Result()
	assert.InDelta(t, 20*math.Log10(0.05)-3.01, r.Integrated, 0.1)
	assert.InDelta(t, 20*math.Log10(0.5), r.TruePeak, 0.3)
}

func TestLoudnessRange(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		levels    []float64
		lra       float64
		low, high float64
	}{
		{name: "nothing above the gate", levels: []float64{-80, Floor}, lra: 0, low: Floor, high: Floor},
		{name: "one level", levels: []float64{-20}, lra: 0, low: -20, high: -20},
		{
			name:   "relative gate drops the quiet part",
			levels: []float64{-60, -30, -30, -30, -30, -25, -25, -25, -25, -20, -20, -20},
			lra:    10, low: -30, high: -20,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lra, low, high := loudnessRange(testCase.levels)
			assert.InDelta(t, testCase.lra, lra, 1e-9)
			assert.InDelta(t, testCase.low, low, 1e-9)
			assert.InDelta(t, testCase.high, high, 1e-9)
		})
	}
}

func TestHelpers(
	t *testing.T,
) {
	assert.Equal(t, Floor, Decibels(0))
	assert.Equal(t, Floor, Decibels(1e-9))
	assert.InDelta(t, -6.02, Decibels(0.5), 0.01)
	assert.Equal(t, Floor, level(0))
	assert.Equal(t, Floor, maxLevel(nil))
	assert.Equal(t, 4800, StepSamples(48000))
	assert.Equal(t, 4410, StepSamples(44100))
	assert.Equal(t, 1, StepSamples(0))
	assert.InDelta(t, 1.23, Round(1.2345), 1e-12)
	assert.Empty(t, complete([]float64{1, 2}, momentarySteps))
}

func TestPeakMeterFlush(
	t *testing.T,
) {
	fast := newPeakMeter(192000)
	assert.InDelta(t, 0.5, fast.peak([]float32{0.1, -0.5}), 1e-9)
	assert.Zero(t, fast.flush())

	slow := newPeakMeter(48000)
	assert.Positive(t, slow.peak([]float32{0, 0, 1}))
	assert.Positive(t, slow.flush())
}
