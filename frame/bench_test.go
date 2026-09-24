package frame

import "testing"

func BenchmarkBuildThumb1080p(
	b *testing.B,
) {
	pool := NewPool(1920, 1080, PoolOptions{ThumbMaxWidth: 240})
	f := pool.Get()

	for i := range f.Luma.Pix {
		f.Luma.Pix[i] = byte(i)
	}

	for b.Loop() {
		pool.BuildThumb(f)
	}
}
