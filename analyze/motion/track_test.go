package motion

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/media"
)

func TestInterpolate(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		values []float64
		valid  []bool
		want   []float64
	}{
		{name: "nothing valid", values: []float64{1, 2}, valid: []bool{false, false}, want: []float64{1, 2}},
		{
			name:   "ends and gaps",
			values: []float64{9, 2, 9, 9, 8, 9},
			valid:  []bool{false, true, false, false, true, false},
			want:   []float64{2, 2, 4, 6, 8, 8},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			interpolate(testCase.values, testCase.valid)
			assert.Equal(t, testCase.want, testCase.values)
		})
	}
}

func TestMovingAverage(
	t *testing.T,
) {
	values := []float64{0, 3, 6, 3, 0}
	movingAverage(values, 3)

	assert.Equal(t, []float64{1.5, 3, 4, 3, 1.5}, values)
}

func TestJitter(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		dx, dy []float64
		want   func(t *testing.T, got []float64)
	}{
		{
			name: "a steady move has no jitter, up to the edges",
			dx:   []float64{2, 2, 2, 2, 2, 2, 2, 2},
			dy:   []float64{-1, -1, -1, -1, -1, -1, -1, -1},
			want: func(t *testing.T, got []float64) {
				for _, v := range got {
					assert.InDelta(t, 0, v, 1e-9)
				}
			},
		},
		{
			name: "back and forth jitters",
			dx:   []float64{1, -1, 1, -1, 1, -1, 1, -1},
			dy:   make([]float64, 8),
			want: func(t *testing.T, got []float64) {
				assert.Greater(t, got[3], 0.3)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.want(t, jitter(testCase.dx, testCase.dy, 5))
		})
	}
}

func TestWindow(
	t *testing.T,
) {
	testCases := []struct {
		name          string
		seconds, step float64
		want          int
	}{
		{name: "half a second at 25 fps", seconds: 0.5, step: 0.04, want: 13},
		{name: "shorter than a frame", seconds: 0.001, step: 0.04, want: 1},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, window(testCase.seconds, testCase.step))
		})
	}
}

func TestMedian(
	t *testing.T,
) {
	values := []float64{3, 1, 2}

	assert.InDelta(t, 2.0, median(values), 1e-9)
	assert.Equal(t, []float64{3, 1, 2}, values, "values are not modified")
	assert.Zero(t, median(nil))
}

func TestFrameDurations(
	t *testing.T,
) {
	testCases := []struct {
		name string
		pts  []media.Duration
		want []float64
	}{
		{name: "single frame", pts: []media.Duration{0}, want: []float64{fallbackFrameDuration}},
		{
			name: "repeated timestamp takes the median",
			pts:  []media.Duration{0, media.Seconds(0.04), media.Seconds(0.04), media.Seconds(0.08)},
			want: []float64{0.04, 0.04, 0.04, 0.04},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := frameDurations(testCase.pts)
			assert.InDeltaSlice(t, testCase.want, got, 1e-9)
		})
	}
}
