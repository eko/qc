package loudness

import (
	"math"
	"testing"
)

// benchSignal is 100 ms of stereo noise-like signal at 48 kHz, the chunk
// the decoder hands over.
func benchSignal() [][]float32 {
	const frames = 4800

	out := [][]float32{make([]float32, frames), make([]float32, frames)}
	for c := range out {
		for i := range out[c] {
			out[c][i] = float32(0.3 * math.Sin(float64(i*(c+3))*0.37))
		}
	}

	return out
}

func BenchmarkMeterAdd(b *testing.B) {
	samples := benchSignal()
	m := NewMeter(48000, []float64{1, 1})

	b.SetBytes(int64(len(samples) * len(samples[0]) * 4))

	for b.Loop() {
		_ = m.Add(samples)
	}
}

func BenchmarkTruePeak(b *testing.B) {
	samples := benchSignal()[0]
	p := newPeakMeter(48000)

	b.SetBytes(int64(len(samples) * 4))

	for b.Loop() {
		_ = p.peak(samples)
	}
}

func BenchmarkKWeighting(b *testing.B) {
	samples := benchSignal()[0]
	f := newKFilter(48000)

	b.SetBytes(int64(len(samples) * 4))

	for b.Loop() {
		_ = f.energy(samples)
	}
}
