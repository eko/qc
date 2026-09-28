// Package audiotest synthesises test signals: the conformance signals of
// EBU Tech 3341 (loudness meters, true peak) and Tech 3342 (loudness
// range) as their tables describe them, with the expected readings and
// tolerances of the specifications, and PCM streams with known defects.
// It serves the tests of the audio packages and the audio validation tool
// (bench/audioval).
package audiotest

import (
	"encoding/binary"
	"math"
)

// Rate is the sample rate of the conformance signals (48 kHz, as in the
// EBU test files).
const Rate = 48000

// toneFrequency is the 1 kHz of the EBU sine signals.
const toneFrequency = 1000

// Tone is a stretch of a sine signal: its peak level (dBFS) and length.
type Tone struct {
	Level   float64
	Seconds float64
}

// Sine synthesises a sine at freq (Hz) sampled at rate, starting at phase
// (degrees), whose level changes stretch by stretch without breaking its
// phase.
func Sine(
	rate int,
	freq, phase float64,
	tones ...Tone,
) []float32 {
	var out []float32

	step := 2 * math.Pi * freq / float64(rate)
	start := phase * math.Pi / 180

	for _, t := range tones {
		amplitude := math.Pow(10, t.Level/20)
		n := int(math.Round(t.Seconds * float64(rate)))

		for range n {
			out = append(out, float32(amplitude*math.Sin(start+step*float64(len(out)))))
		}
	}

	return out
}

// Channels returns n channels carrying x (the same slice: callers must not
// modify it).
func Channels(
	n int,
	x []float32,
) [][]float32 {
	out := make([][]float32, n)
	for c := range out {
		out[c] = x
	}

	return out
}

// Repeat repeats the tones n times.
func Repeat(
	n int,
	tones ...Tone,
) []Tone {
	out := make([]Tone, 0, n*len(tones))
	for range n {
		out = append(out, tones...)
	}

	return out
}

// Fade shapes the first and last seconds of x with raised-cosine ramps, in
// place, and returns it: an abrupt onset is not band-limited, and its
// reconstruction rings above the waveform's peak.
func Fade(
	x []float32,
	rate int,
	seconds float64,
) []float32 {
	n := min(int(seconds*float64(rate)), len(x)/2)
	head, tail := x[:n], x[len(x)-n:]

	for i := range head {
		g := float32(0.5 - 0.5*math.Cos(math.Pi*float64(i)/float64(n)))
		head[i] *= g
		tail[n-1-i] *= g
	}

	return x
}

// WAV format constants: WAVE_FORMAT_EXTENSIBLE carrying IEEE float samples,
// whose channel mask tells ffmpeg the layout.
const (
	wavExtensible  = 0xFFFE
	wavFloat       = 3
	wavHeaderSize  = 68
	wavFormatSize  = 40
	wavExtraSize   = 22
	wavSampleBits  = 32
	wavSampleBytes = 4
)

// Channel masks of the layouts the tests write (WAVEFORMATEXTENSIBLE).
const (
	MaskMono     = 0x4
	MaskStereo   = 0x3
	Mask51       = 0x3F
	Mask51Side   = 0x60F
	maskSubtypes = 0x00100000
)

// WAV encodes planar channels as a 32-bit float WAV file with the given
// channel mask.
func WAV(
	rate int,
	mask uint32,
	channels [][]float32,
) []byte {
	frames := 0
	if len(channels) > 0 {
		frames = len(channels[0])
	}

	dataSize := frames * len(channels) * wavSampleBytes
	out := make([]byte, 0, wavHeaderSize+dataSize)
	le := binary.LittleEndian

	out = append(out, "RIFF"...)
	out = le.AppendUint32(out, u32(wavHeaderSize-8+dataSize))
	out = append(out, "WAVEfmt "...)
	out = le.AppendUint32(out, wavFormatSize)
	out = le.AppendUint16(out, wavExtensible)
	out = le.AppendUint16(out, u16(len(channels)))
	out = le.AppendUint32(out, u32(rate))
	out = le.AppendUint32(out, u32(rate*len(channels)*wavSampleBytes))
	out = le.AppendUint16(out, u16(len(channels)*wavSampleBytes))
	out = le.AppendUint16(out, wavSampleBits)
	out = le.AppendUint16(out, wavExtraSize)
	out = le.AppendUint16(out, wavSampleBits)
	out = le.AppendUint32(out, mask)
	// KSDATAFORMAT_SUBTYPE_IEEE_FLOAT: the format code, then the fixed
	// GUID suffix.
	out = le.AppendUint32(out, wavFloat)
	out = le.AppendUint32(out, maskSubtypes)
	out = append(out, 0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71)
	out = append(out, "data"...)
	out = le.AppendUint32(out, u32(dataSize))

	for i := range frames {
		for _, ch := range channels {
			out = le.AppendUint32(out, math.Float32bits(ch[i]))
		}
	}

	return out
}

// u32 and u16 convert WAV header fields: test signals stay far below the
// 4 GiB and 65 535 channels a WAV file can describe.
func u32(
	n int,
) uint32 {
	return uint32(n) //nolint:gosec // bounded by the test signals
}

func u16(
	n int,
) uint16 {
	return uint16(n) //nolint:gosec // bounded by the test signals
}
