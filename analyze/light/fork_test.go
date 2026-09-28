package light

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
	pool := frame.NewPool(80, 50, frame.PoolOptions{SampleStep: 2})
	frames := analyzetest.Frames(pool, 20, 25, func(f *frame.Frame, i int) {
		fill(&f.Samples.Y, 300+i*20)
		fill(&f.Samples.Cb, 512)
		fill(&f.Samples.Cr, 512+i)
	})
	defer analyzetest.Release(frames)

	seq, split := New(media.Color{Transfer: media.TransferPQ}), New(media.Color{Transfer: media.TransferPQ})
	analyzetest.Split(t, seq, split, frames, 7, 13)

	want := seq.Result()
	assert.Equal(t, 19, want.MaxCLLFrame)
	assert.Equal(t, want, split.Result())
}
