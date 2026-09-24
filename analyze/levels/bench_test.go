package levels

import (
	"testing"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/media"
)

func BenchmarkConsume1080p(b *testing.B) {
	pool := frame.NewPool(1920, 1080, frame.PoolOptions{ThumbMaxWidth: 240})
	f := pool.Get()

	for i := range f.Luma.Pix {
		f.Luma.Pix[i] = byte(i * 7)
	}

	a := New(media.LevelsFor("tv"))

	for b.Loop() {
		_ = a.Consume(f)
	}
}
