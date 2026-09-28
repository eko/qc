package motion

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// field samples the displacement field of m on a 6×4 grid of blocks.
func field(
	m similarity,
) []vector {
	var out []vector

	for y := -45.0; y <= 45; y += 30 {
		for x := -100.0; x <= 100; x += 40 {
			at := complex(x, y)
			out = append(out, vector{at: at, d: m.at(at)})
		}
	}

	return out
}

func TestFitter(
	t *testing.T,
) {
	zoom := similarity{t: complex(1.5, -0.5), z: complex(0.004, 0.001)}

	withObject := field(similarity{})
	for i := range 6 {
		// A moving object over a quarter of the blocks.
		withObject[i].d = complex(3, 0)
	}

	testCases := []struct {
		name        string
		vectors     []vector
		want        similarity
		wantInliers int
		wantOK      bool
	}{
		{name: "too few vectors", vectors: field(zoom)[:3]},
		{name: "exact similarity", vectors: field(zoom), want: zoom, wantInliers: 24, wantOK: true},
		{name: "moving object is an outlier", vectors: withObject, want: similarity{}, wantInliers: 18, wantOK: true},
		{
			name:    "clustered blocks fall back on the median translation",
			vectors: []vector{{at: 0, d: 1}, {at: 1, d: 1}, {at: 2, d: 1}, {at: 3, d: 1}},
			want:    similarity{t: 1}, wantInliers: 4, wantOK: true,
		},
		{
			name: "no consensus",
			vectors: []vector{
				{at: -100, d: 5}, {at: -50, d: -7i}, {at: 0, d: 9}, {at: 50, d: 3 + 4i}, {at: 100, d: -6}, {at: 30i, d: 12i},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var f fitter

			got, ok := f.fit(testCase.vectors)
			assert.Equal(t, testCase.wantOK, ok)

			if !ok {
				return
			}

			assert.InDelta(t, real(testCase.want.t), real(got.model.t), 1e-9)
			assert.InDelta(t, imag(testCase.want.t), imag(got.model.t), 1e-9)
			assert.InDelta(t, real(testCase.want.z), real(got.model.z), 1e-9)
			assert.InDelta(t, imag(testCase.want.z), imag(got.model.z), 1e-9)
			assert.Equal(t, testCase.wantInliers, got.inliers)
		})
	}
}

func TestFromPair(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		a, b   vector
		wantOK bool
	}{
		{name: "distant blocks", a: vector{at: -50}, b: vector{at: 50}, wantOK: true},
		{name: "blocks too close", a: vector{at: 0}, b: vector{at: 5}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, ok := fromPair(testCase.a, testCase.b)
			assert.Equal(t, testCase.wantOK, ok)
		})
	}
}

func TestLeastSquaresTooFewInliers(
	t *testing.T,
) {
	vs := []vector{{at: -50, d: 5}, {at: 50, d: -5}, {at: 0, d: 0}}

	m, ok := leastSquares(similarity{}, vs)
	assert.False(t, ok)
	assert.Equal(t, similarity{}, m)
}
