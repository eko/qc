package defect

import (
	"testing"

	"github.com/eko/qc/internal/audiotest"
)

// BenchmarkAdd measures 100 ms of stereo programme, the chunk the decoder
// hands over.
func BenchmarkAdd(b *testing.B) {
	ch := audiotest.Programme(rate, 0.1, 1)
	d := New(rate, 2, [][2]int{{0, 1}}, Options{})

	b.SetBytes(int64(len(ch) * len(ch[0]) * 4))

	for b.Loop() {
		_ = d.Add(ch)
	}
}
