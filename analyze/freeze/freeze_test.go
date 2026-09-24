package freeze

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/media"
)

func TestAnalyzer(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		opts   Options
		values []byte
		want   []media.Interval
	}{
		{
			name:   "a still run long enough is reported from its first frame",
			opts:   Options{MinDuration: media.Seconds(0.3)},
			values: []byte{10, 20, 50, 50, 50, 50, 60},
			want:   []media.Interval{{Start: media.Seconds(0.2), End: media.Seconds(0.6)}},
		},
		{
			name:   "moving frames are never frozen",
			opts:   Options{MinDuration: media.Seconds(0.1)},
			values: []byte{10, 20, 30, 40},
		},
		{
			name:   "a short still run is dropped with the 2s default",
			values: []byte{50, 50, 50, 50},
		},
		{
			name:   "the tolerance absorbs small changes",
			opts:   Options{MaxDiff: 1.5, MinDuration: media.Seconds(0.1)},
			values: []byte{50, 51, 50, 90},
			want:   []media.Interval{{Start: 0, End: media.Seconds(0.3)}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pool := frame.NewPool(8, 8, frame.PoolOptions{ThumbMaxWidth: 4})
			a := New(testCase.opts)

			for i, v := range testCase.values {
				f := pool.Get()
				f.Index, f.PTS = i, media.Seconds(float64(i)/10)

				for p := range f.Luma.Pix {
					f.Luma.Pix[p] = v
				}

				pool.BuildThumb(f)
				require.NoError(t, a.Consume(f))
				f.Release()
			}

			require.NoError(t, a.Close())

			got := a.Result(media.Seconds(float64(len(testCase.values)) / 10))
			assert.Equal(t, testCase.want, got.Segments)
		})
	}
}
