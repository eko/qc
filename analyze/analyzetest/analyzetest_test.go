package analyzetest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/analyze/analyzetest"
	"github.com/eko/qc/analyze/scene"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/media"
)

func TestSplit(
	t *testing.T,
) {
	pool := frame.NewPool(8, 8, frame.PoolOptions{})
	frames := analyzetest.Frames(pool, 12, 4, func(f *frame.Frame, i int) {
		for p := range f.Luma.Pix {
			f.Luma.Pix[p] = byte(i / 6 * 100)
		}
	})
	defer analyzetest.Release(frames)

	assert.Equal(t, media.Seconds(2.75), frames[11].PTS)

	seq, split := scene.New(scene.Options{}), scene.New(scene.Options{})
	analyzetest.Split(t, seq, split, frames, 6)

	assert.Equal(t, seq.Result(media.Seconds(3)), split.Result(media.Seconds(3)))
}
