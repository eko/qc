package black

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/media"
)

// feed consumes one 8×8 frame per value, at 10 fps, and returns the result.
func feed(
	t *testing.T,
	a *Analyzer,
	pool *frame.Pool,
	values []byte,
) Result {
	t.Helper()

	for i, v := range values {
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

	return a.Result(media.Seconds(float64(len(values)) / 10))
}

func TestAnalyzer(
	t *testing.T,
) {
	tv := media.LevelsFor("tv")

	testCases := []struct {
		name   string
		levels media.Levels
		opts   Options
		thumbs bool
		values []byte
		want   []media.Interval
	}{
		{
			name:   "a black run long enough is reported",
			levels: tv,
			thumbs: true,
			values: []byte{128, 128, 16, 16, 20, 30, 16, 16, 128, 128},
			// 16 + 10% of 219 = 37.9: every dark value is below the threshold.
			want: []media.Interval{{Start: media.Seconds(0.2), End: media.Seconds(0.8)}},
		},
		{
			name:   "a short run is dropped",
			levels: tv,
			thumbs: true,
			values: []byte{128, 16, 16, 128},
		},
		{
			name:   "the pixel threshold is relative to the levels",
			levels: media.LevelsFor("pc"),
			opts:   Options{MinDuration: media.Seconds(0.1)},
			thumbs: true,
			// 0 + 10% of 255 = 25.5: 30 is not dark on a full range signal.
			values: []byte{128, 30, 20, 128},
			want:   []media.Interval{{Start: media.Seconds(0.2), End: media.Seconds(0.3)}},
		},
		{
			name:   "the luma is used without thumbnails",
			levels: tv,
			opts:   Options{MinDuration: media.Seconds(0.1)},
			values: []byte{128, 16, 128},
			want:   []media.Interval{{Start: media.Seconds(0.1), End: media.Seconds(0.2)}},
		},
		{
			name:   "a run up to the end is closed at the end",
			levels: tv,
			opts:   Options{MinDuration: media.Seconds(0.1), PixelThreshold: 0.05, PictureRatio: 0.5},
			thumbs: true,
			values: []byte{128, 16},
			want:   []media.Interval{{Start: media.Seconds(0.1), End: media.Seconds(0.2)}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			poolOpts := frame.PoolOptions{}
			if testCase.thumbs {
				poolOpts.ThumbMaxWidth = 4
			}

			a := New(testCase.levels, testCase.opts)
			got := feed(t, a, frame.NewPool(8, 8, poolOpts), testCase.values)

			assert.Equal(t, testCase.want, got.Segments)
		})
	}
}

func TestPictureRatio(
	t *testing.T,
) {
	pool := frame.NewPool(10, 1, frame.PoolOptions{})
	a := New(media.LevelsFor("tv"), Options{MinDuration: media.Seconds(0.1)})

	f := pool.Get()
	copy(f.Luma.Pix, []byte{16, 16, 16, 16, 16, 16, 16, 16, 16, 200})
	require.NoError(t, a.Consume(f))
	f.Release()
	require.NoError(t, a.Close())

	frames := a.series.Merge()
	require.Len(t, frames, 1)
	assert.False(t, frames[0].black, "90% of dark pixels is below the 98% default")
}

func TestNewClampsThreshold(
	t *testing.T,
) {
	a := New(media.LevelsFor("pc"), Options{PixelThreshold: 2})

	assert.Equal(t, byte(255), a.threshold)
}
