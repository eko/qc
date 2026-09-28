package crop

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
	const width, height = 32, 24

	pool := frame.NewPool(width, height, frame.PoolOptions{})
	frames := analyzetest.Frames(pool, 45, 25, func(f *frame.Frame, i int) {
		for y := range height {
			for x := range width {
				v := byte(16)
				// Letterboxed content, fully black frames 20 to 29.
				if y >= 4 && y < height-4 && (i < 20 || i >= 30) {
					v = byte(60 + (x+i)%50)
				}

				f.Luma.Pix[y*width+x] = v
			}
		}
	})
	defer analyzetest.Release(frames)

	seq := New(width, height, media.LevelsFor("tv"), Options{})
	split := New(width, height, media.LevelsFor("tv"), Options{})
	// Frame 10 and 30, analysed ones, are shared by two runs.
	analyzetest.Split(t, seq, split, frames, 10, 30)

	want := seq.Result()
	assert.Equal(t, 4, want.FramesUsed)
	assert.True(t, want.Letterbox)
	assert.Equal(t, want, split.Result())
}
