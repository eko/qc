package freeze

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
		// Frame 10 is the first frozen one: its predecessor starts the
		// run, in the previous segment.
		{name: "a run starts on the first repeat", starts: []int{10, 30}},
		{name: "a run starts inside the freeze", starts: []int{20}},
	}

	pool := frame.NewPool(16, 8, frame.PoolOptions{ThumbMaxWidth: 8})

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			frames := analyzetest.Frames(pool, 40, 10, func(f *frame.Frame, i int) {
				v := i
				if i >= 9 && i < 35 {
					v = 9
				}

				for p := range f.Luma.Pix {
					f.Luma.Pix[p] = byte(v * 7)
				}
			})
			defer analyzetest.Release(frames)

			seq, split := New(Options{}), New(Options{})
			analyzetest.Split(t, seq, split, frames, testCase.starts...)

			end := media.Seconds(4)
			want := seq.Result(end)
			assert.Equal(t, []media.Interval{{Start: media.Seconds(0.9), End: media.Seconds(3.5)}}, want.Segments)
			assert.Equal(t, want, split.Result(end))
		})
	}
}
