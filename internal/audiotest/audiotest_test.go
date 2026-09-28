package audiotest

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/audio/loudness"
)

func TestSine(
	t *testing.T,
) {
	x := Sine(8, 2, 90, Tone{Level: 0, Seconds: 1}, Tone{Level: -6.0206, Seconds: 0.5})

	require.Len(t, x, 12)
	assert.InDelta(t, 1, x[0], 1e-6, "starts at its phase")
	assert.InDelta(t, -1, x[2], 1e-6)
	assert.InDelta(t, 0.5, x[8], 1e-4, "the phase runs on across tones")
}

func TestChannelsAndRepeat(
	t *testing.T,
) {
	x := []float32{1, 2}
	ch := Channels(3, x)
	require.Len(t, ch, 3)
	assert.Equal(t, x, ch[2])

	assert.Equal(t, []Tone{{-1, 1}, {-2, 2}, {-1, 1}, {-2, 2}}, Repeat(2, Tone{-1, 1}, Tone{-2, 2}))
}

func TestFade(
	t *testing.T,
) {
	x := []float32{1, 1, 1, 1, 1, 1}
	Fade(x, 2, 1)

	assert.Equal(t, []float32{0, 0.5, 1, 1, 0.5, 0}, x)
	assert.Len(t, Fade([]float32{1}, 48000, 1), 1, "a ramp is at most half the signal")
}

func TestWAV(
	t *testing.T,
) {
	data := WAV(48000, Mask51, [][]float32{{0.5, -1}, {0.25, 1}})
	le := binary.LittleEndian

	require.Len(t, data, wavHeaderSize+16)
	assert.Equal(t, "RIFF", string(data[:4]))
	assert.Equal(t, uint32(len(data)-8), le.Uint32(data[4:]))
	assert.Equal(t, uint16(wavExtensible), le.Uint16(data[20:]))
	assert.Equal(t, uint16(2), le.Uint16(data[22:]))
	assert.Equal(t, uint32(48000), le.Uint32(data[24:]))
	assert.Equal(t, uint32(Mask51), le.Uint32(data[40:]))
	assert.Equal(t, "data", string(data[60:64]))
	assert.Equal(t, uint32(16), le.Uint32(data[64:]))
	assert.Equal(t, float32(0.25), math.Float32frombits(le.Uint32(data[72:])), "samples interleaved")

	assert.Len(t, WAV(48000, MaskMono, nil), wavHeaderSize)
}

func TestProgramme(
	t *testing.T,
) {
	a, b := Programme(8000, 1, 3), Programme(8000, 1, 3)
	require.Len(t, a, 2)
	assert.Len(t, a[0], 8000)
	assert.Equal(t, a, b, "deterministic")
	assert.NotEqual(t, a, Programme(8000, 1, 4))
}

func TestDefects(
	t *testing.T,
) {
	x := []float32{0.5, 0.5, 0.5, 0.5}

	Invert(x, 2, 0.5, 1)
	assert.Equal(t, []float32{0.5, -0.5, 0.5, 0.5}, x)

	Clip(x, 2, 0, 1, 4)
	assert.Equal(t, []float32{1, -1, 0.5, 0.5}, x)

	Mute(x, 2, 1, 5)
	assert.Equal(t, []float32{1, -1, 0, 0}, x)

	Offset(x, 0.25)
	assert.Equal(t, []float32{1.25, -0.75, 0.25, 0.25}, x)
}

func TestCheck(
	t *testing.T,
) {
	r := loudness.Result{
		Integrated: -23, Range: 5, TruePeak: -1,
		Series: loudness.Series{Momentary: []float64{-60, -60, -60, -23.05, -22.9}, ShortTerm: append(make([]float64, 29), -23, -23.02)},
	}

	testCases := []struct {
		name  string
		check Check
		want  float64
		pass  bool
	}{
		{name: "integrated", check: meter(Integrated, -23), want: -23, pass: true},
		{name: "range", check: Check{Measure: Range, Want: 7, Below: 1, Above: 1}, want: 5},
		{name: "true peak", check: Check{Measure: TruePeak, Want: -1.1, Below: 0.4, Above: 0.2}, want: -1, pass: true},
		{name: "momentary, the furthest complete window", check: meter(Momentary, -23), want: -22.9, pass: true},
		{name: "short-term", check: meter(ShortTerm, -23), want: -23.02, pass: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := testCase.check.Reading(r)
			assert.InDelta(t, testCase.want, got, 1e-9)
			assert.Equal(t, testCase.pass, testCase.check.Pass(got))
		})
	}

	assert.True(t, math.IsNaN(Check{Measure: "?"}.Reading(r)))
	assert.Equal(t, "±0.1", meter(Integrated, 0).Tolerance())
	assert.Equal(t, "+0.2/−0.4", Check{Below: 0.4, Above: 0.2}.Tolerance())
}

func TestCases(
	t *testing.T,
) {
	cases := append(Tech3341(), Tech3342()...)
	require.Len(t, cases, 17)

	for _, c := range cases {
		signal := c.Signal()
		assert.Len(t, signal, len(c.Weights), c.Name)
		assert.NotEmpty(t, c.Checks, c.Name)
		assert.NotEmpty(t, c.Description, c.Name)
	}
}
