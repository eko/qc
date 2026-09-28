package black

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
	pool := frame.NewPool(16, 8, frame.PoolOptions{ThumbMaxWidth: 8})
	frames := analyzetest.Frames(pool, 40, 10, func(f *frame.Frame, i int) {
		v := byte(120)
		if i >= 10 && i < 25 {
			v = 16
		}

		for p := range f.Luma.Pix {
			f.Luma.Pix[p] = v
		}
	})
	defer analyzetest.Release(frames)

	seq, split := New(media.LevelsFor("tv"), Options{}), New(media.LevelsFor("tv"), Options{})
	analyzetest.Split(t, seq, split, frames, 10, 17, 25)

	end := media.Seconds(4)
	want := seq.Result(end)
	assert.Len(t, want.Segments, 1)
	assert.Equal(t, want, split.Result(end))
}
