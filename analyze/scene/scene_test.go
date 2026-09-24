package scene

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/media"
)

func timeline(
	n int,
) []media.Duration {
	pts := make([]media.Duration, n)
	for i := range pts {
		pts[i] = media.Seconds(float64(i) / 25)
	}

	return pts
}

func TestDetectCuts(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		scores func() []float64
		opts   Options
		want   []int
	}{
		{
			name:   "a cut without neighbours only needs the absolute floor",
			scores: func() []float64 { return []float64{0, 40} },
			opts:   Options{MinShot: media.Seconds(0.01)},
			want:   []int{1},
		},
		{
			name: "isolated spikes are cuts",
			scores: func() []float64 {
				s := make([]float64, 100)
				for i := range s {
					s[i] = 2
				}

				s[30], s[70] = 40, 35

				return s
			},
			want: []int{30, 70},
		},
		{
			name: "sustained high motion is not a cut",
			scores: func() []float64 {
				s := make([]float64, 100)
				for i := range s {
					s[i] = 30
				}

				return s
			},
			want: nil,
		},
		{
			name: "cuts closer than the minimum shot are merged",
			scores: func() []float64 {
				s := make([]float64, 100)
				s[30], s[35] = 40, 40

				return s
			},
			want: []int{30},
		},
		{
			name: "small spikes are ignored",
			scores: func() []float64 {
				s := make([]float64, 100)
				s[50] = 8

				return s
			},
			want: nil,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			scores := testCase.scores()
			assert.Equal(t, testCase.want, DetectCuts(scores, timeline(len(scores)), testCase.opts))
		})
	}
}

func TestShots(
	t *testing.T,
) {
	pts := timeline(100)
	got := shots([]int{25, 60}, pts, media.Seconds(4))

	assert.Equal(t, []Shot{
		{Interval: media.Interval{Start: 0, End: pts[25]}, FirstFrame: 0, LastFrame: 24},
		{Interval: media.Interval{Start: pts[25], End: pts[60]}, FirstFrame: 25, LastFrame: 59},
		{Interval: media.Interval{Start: pts[60], End: media.Seconds(4)}, FirstFrame: 60, LastFrame: 99},
	}, got)
}
