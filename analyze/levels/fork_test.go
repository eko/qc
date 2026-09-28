package levels

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/analyze/analyzetest"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/media"
)

func TestForksMatchSequentialPass(
	t *testing.T,
) {
	pool := frame.NewPool(37, 5, frame.PoolOptions{})
	frames := analyzetest.Frames(pool, 30, 25, func(f *frame.Frame, i int) {
		for p := range f.Luma.Pix {
			f.Luma.Pix[p] = byte(p*i + i)
		}
	})
	defer analyzetest.Release(frames)

	seq, split := New(media.LevelsFor("tv")), New(media.LevelsFor("tv"))
	analyzetest.Split(t, seq, split, frames, 11, 20)

	want := seq.Result()
	assert.Len(t, want.Mean, 30)
	assert.Equal(t, want, split.Result())
}
