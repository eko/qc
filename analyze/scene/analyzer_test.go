package scene

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
		name       string
		values     []byte
		wantScores []float64
		wantShots  []Shot
	}{
		{
			name:   "no frame",
			values: nil,
		},
		{
			name:       "a hard cut splits two shots",
			values:     []byte{10, 10, 10, 10, 10, 10, 200, 200, 200, 200, 200, 200},
			wantScores: []float64{0, 0, 0, 0, 0, 0, 190, 0, 0, 0, 0, 0},
			wantShots: []Shot{
				{Interval: media.Interval{Start: 0, End: media.Seconds(0.6)}, FirstFrame: 0, LastFrame: 5},
				{Interval: media.Interval{Start: media.Seconds(0.6), End: media.Seconds(1.2)}, FirstFrame: 6, LastFrame: 11},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pool := frame.NewPool(8, 8, frame.PoolOptions{ThumbMaxWidth: 4})
			a := New(Options{MinShot: media.Seconds(0.2)})

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
			assert.Equal(t, testCase.wantScores, got.Scores)
			assert.Equal(t, testCase.wantShots, got.Shots)
		})
	}
}

func TestMeanAbsDiff(
	t *testing.T,
) {
	testCases := []struct {
		name string
		a, b []byte
		want float64
	}{
		{name: "empty", want: 0},
		{name: "identical", a: []byte{1, 2, 3}, b: []byte{1, 2, 3}, want: 0},
		{name: "signs do not cancel", a: []byte{10, 20}, b: []byte{20, 10}, want: 10},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, MeanAbsDiff(testCase.a, testCase.b), 1e-9)
		})
	}
}
