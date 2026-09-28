package siti

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/analyze/analyzetest"
	"github.com/eko/qc/frame"
)

func TestForksMatchSequentialPass(
	t *testing.T,
) {
	pool := frame.NewPool(40, 20, frame.PoolOptions{})
	frames := analyzetest.Frames(pool, 30, 25, func(f *frame.Frame, i int) {
		for p := range f.Luma.Pix {
			f.Luma.Pix[p] = byte((p*(i+3))%251 + i)
		}
	})
	defer analyzetest.Release(frames)

	seq, split := New(3), New(3)
	analyzetest.Split(t, seq, split, frames, 9, 21)

	want := seq.Result()
	assert.Len(t, want.TI, 30)
	assert.Equal(t, want, split.Result())
}
