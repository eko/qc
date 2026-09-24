package siti

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/frame"
)

func plane(
	w, h int,
	fn func(x, y int) byte,
) *frame.Plane {
	p := &frame.Plane{Width: w, Height: h, Stride: w, Pix: make([]byte, w*h)}
	for y := range h {
		for x := range w {
			p.Pix[y*w+x] = fn(x, y)
		}
	}

	return p
}

func TestSpatialInformation(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		plane *frame.Plane
		want  float64
	}{
		{
			name:  "too small for a Sobel kernel",
			plane: plane(2, 16, func(x, _ int) byte { return byte(x * 100) }),
			want:  0,
		},
		{
			name:  "flat",
			plane: plane(16, 16, func(int, int) byte { return 128 }),
			want:  0,
		},
		{
			// Horizontal ramp of slope 1: Sobel gx = 8 everywhere, so the
			// magnitude is constant and its standard deviation is 0.
			name:  "linear ramp",
			plane: plane(16, 16, func(x, _ int) byte { return byte(x) }),
			want:  0,
		},
		{
			// Vertical step at x=8 on 18 inner columns: 2 columns see |gx|=400
			// (edge), the rest 0 → std = 400 * sqrt(p(1-p)) with p = 2/16.
			name: "vertical edge",
			plane: plane(18, 10, func(x, _ int) byte {
				if x < 9 {
					return 0
				}

				return 100
			}),
			want: 400 * math.Sqrt(2.0/16*(1-2.0/16)),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, SpatialInformation(testCase.plane), 1e-9)
		})
	}
}

func TestTemporalInformation(
	t *testing.T,
) {
	base := plane(8, 8, func(x, y int) byte { return byte(x + y) })

	testCases := []struct {
		name string
		cur  *frame.Plane
		want float64
	}{
		{name: "identical", cur: base, want: 0},
		{name: "uniform brightening has no motion", cur: plane(8, 8, func(x, y int) byte { return byte(x + y + 10) }), want: 0},
		{
			name: "half the pixels change by 20",
			cur: plane(8, 8, func(x, y int) byte {
				if y < 4 {
					return byte(x + y + 20)
				}

				return byte(x + y)
			}),
			want: 10,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, TemporalInformation(base, testCase.cur), 1e-9)
		})
	}
}

func TestAnalyzerOrdersParallelResults(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		workers int
	}{
		{name: "fixed workers", workers: 4},
		{name: "one worker per CPU", workers: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assertOrdered(t, New(testCase.workers))
		})
	}
}

func TestAnalyzerWithoutFrames(
	t *testing.T,
) {
	a := New(1)
	assert.NoError(t, a.Close())

	res := a.Result()
	assert.Empty(t, res.SI)
	assert.Zero(t, res.TISummary)
}

func assertOrdered(
	t *testing.T,
	a *Analyzer,
) {
	t.Helper()

	pool := frame.NewPool(32, 32, frame.PoolOptions{})

	for i := range 20 {
		f := pool.Get()
		f.Index = i

		for p := range f.Luma.Pix {
			f.Luma.Pix[p] = byte((p * (i + 1)) % 251)
		}

		assert.NoError(t, a.Consume(f))
		f.Release()
	}

	assert.NoError(t, a.Close())

	res := a.Result()
	assert.Len(t, res.SI, 20)
	assert.Zero(t, res.TI[0])

	for i := 1; i < 20; i++ {
		assert.Positive(t, res.TI[i], "frame %d", i)
	}
}
