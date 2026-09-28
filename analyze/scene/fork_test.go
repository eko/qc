package scene

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
	testCases := []struct {
		name   string
		starts []int
	}{
		{name: "a run starts on a cut", starts: []int{12, 30}},
		{name: "runs start inside shots", starts: []int{5, 19}},
		{name: "a single run", starts: nil},
	}

	pool := frame.NewPool(16, 8, frame.PoolOptions{ThumbMaxWidth: 8})

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			frames := analyzetest.Frames(pool, 40, 10, func(f *frame.Frame, i int) {
				base := map[bool]byte{true: 10, false: 200}[i < 12]
				if i >= 25 {
					base = 60
				}

				for p := range f.Luma.Pix {
					f.Luma.Pix[p] = base + byte((p+i)%3)
				}
			})
			defer analyzetest.Release(frames)

			seq, split := New(Options{}), New(Options{})
			analyzetest.Split(t, seq, split, frames, testCase.starts...)

			end := media.Seconds(4)
			want := seq.Result(end)
			assert.Len(t, want.Shots, 3)
			assert.Equal(t, want, split.Result(end))
		})
	}
}
