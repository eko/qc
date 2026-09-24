package linalg

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDot(
	t *testing.T,
) {
	testCases := []struct {
		name string
		a, b []float64
		want float64
	}{
		{name: "empty", want: 0},
		{name: "orthogonal", a: []float64{1, 0}, b: []float64{0, 1}, want: 0},
		{name: "longer b", a: []float64{1, 2}, b: []float64{3, 4, 5}, want: 11},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, Dot(testCase.a, testCase.b), 1e-12)
		})
	}
}

func TestSolve(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		augmented [][]float64
		want      []float64
	}{
		{name: "diagonal", augmented: [][]float64{{2, 0, 4}, {0, 4, 2}}, want: []float64{2, 0.5}},
		{name: "needs pivoting", augmented: [][]float64{{0, 1, 3}, {1, 1, 5}}, want: []float64{2, 3}},
		{name: "a singular column gets a zero coefficient", augmented: [][]float64{{2, 0, 4}, {0, 0, 0}}, want: []float64{2, 0}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			before := clone(testCase.augmented)

			got := Solve(testCase.augmented)
			assert.InDeltaSlice(t, testCase.want, got, 1e-12)
			assert.Equal(t, before, testCase.augmented, "the input is left unchanged")
		})
	}
}

func TestRidge(
	t *testing.T,
) {
	xs := [][]float64{{1, 0}, {1, 1}, {1, 2}, {1, 3}}
	ys := []float64{1, 3, 5, 7}
	ws := []float64{0.25, 0.25, 0.25, 0.25}

	testCases := []struct {
		name      string
		penalty   float64
		want      []float64
		tolerance float64
	}{
		{name: "least squares", want: []float64{1, 2}, tolerance: 1e-9},
		{name: "a small penalty barely shrinks the slope", penalty: 1e-3, want: []float64{1, 2}, tolerance: 1e-2},
		{name: "a large penalty flattens the fit to the mean", penalty: 1e6, want: []float64{4, 0}, tolerance: 1e-3},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDeltaSlice(t, testCase.want, Ridge(xs, ys, ws, testCase.penalty), testCase.tolerance)
		})
	}
}

func TestInvert(
	t *testing.T,
) {
	testCases := []struct {
		name string
		m    [][]float64
	}{
		{name: "symmetric positive-definite", m: [][]float64{{4, 1, 0, 0}, {1, 3, 1, 0}, {0, 1, 2, 1}, {0, 0, 1, 5}}},
		{name: "permutation", m: [][]float64{{0, 1, 0}, {1, 0, 0}, {0, 0, 1}}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			before := clone(testCase.m)
			inv := Invert(testCase.m)

			assert.Equal(t, before, testCase.m, "the input is left unchanged")

			for i := range testCase.m {
				for j := range testCase.m {
					column := make([]float64, len(inv))
					for k := range inv {
						column[k] = inv[k][j]
					}

					want := 0.0
					if i == j {
						want = 1
					}

					assert.InDelta(t, want, Dot(testCase.m[i], column), 1e-12, "row %d col %d", i, j)
				}
			}
		})
	}
}
