// Package linalg is the small dense linear algebra behind the ladder's
// regressions: systems of a handful of unknowns, where a dependency would
// cost more than the few loops here. Every function leaves its inputs
// unchanged.
package linalg

import "math"

// singular is the magnitude under which a pivot counts as zero.
const singular = 1e-12

// Dot returns the dot product of a and b (b at least as long as a).
func Dot(
	a, b []float64,
) float64 {
	s := 0.0
	for i := range a {
		s += a[i] * b[i]
	}

	return s
}

// Solve solves the linear system given as an augmented matrix (each row its
// coefficients then its right-hand side) by Gauss-Jordan elimination with
// partial pivoting. An unknown whose column is singular gets zero: the
// regressions solved here rather drop a degenerate regressor than fail.
func Solve(
	augmented [][]float64,
) []float64 {
	a := clone(augmented)

	for col := range a {
		pivot := col
		for r := col + 1; r < len(a); r++ {
			if math.Abs(a[r][col]) > math.Abs(a[pivot][col]) {
				pivot = r
			}
		}

		a[col], a[pivot] = a[pivot], a[col]
		lead := a[col]

		if math.Abs(lead[col]) < singular {
			continue
		}

		for r, row := range a {
			if r == col {
				continue
			}

			factor := row[col] / lead[col]
			for j := col; j < len(row); j++ {
				row[j] -= factor * lead[j]
			}
		}
	}

	out := make([]float64, len(a))
	for i, row := range a {
		if math.Abs(row[i]) >= singular {
			out[i] = row[len(row)-1] / row[i]
		}
	}

	return out
}

// Ridge solves the weighted ridge regression of ys on the rows of xs
// (weights ws): the coefficients minimising Σ w (y − x·β)² + penalty·|β|²,
// the first column, the constant, not penalised. Few observations and
// correlated regressors are what the penalty is for.
func Ridge(
	xs [][]float64,
	ys, ws []float64,
	penalty float64,
) []float64 {
	n := len(xs[0])
	a := make([][]float64, n)

	for i := range a {
		a[i] = make([]float64, n+1)
		if i > 0 {
			a[i][i] = penalty
		}
	}

	for r, x := range xs {
		for i := range n {
			for j := range n {
				a[i][j] += ws[r] * x[i] * x[j]
			}

			a[i][n] += ws[r] * x[i] * ys[r]
		}
	}

	return Solve(a)
}

// Invert inverts a square matrix by Gauss-Jordan elimination with partial
// pivoting. The matrix must be invertible: the ladder only inverts
// symmetric positive-definite matrices, whose pivots never vanish.
func Invert(
	m [][]float64,
) [][]float64 {
	a := clone(m)
	n := len(a)

	inv := make([][]float64, n)
	for i := range inv {
		inv[i] = make([]float64, n)
		inv[i][i] = 1
	}

	for col := range n {
		pivot := col
		for r := col + 1; r < n; r++ {
			if math.Abs(a[r][col]) > math.Abs(a[pivot][col]) {
				pivot = r
			}
		}

		a[col], a[pivot] = a[pivot], a[col]
		inv[col], inv[pivot] = inv[pivot], inv[col]

		scale := a[col][col]
		for j := range n {
			a[col][j] /= scale
			inv[col][j] /= scale
		}

		for r := range n {
			if r == col {
				continue
			}

			factor := a[r][col]
			for j := range n {
				a[r][j] -= factor * a[col][j]
				inv[r][j] -= factor * inv[col][j]
			}
		}
	}

	return inv
}

// clone deep-copies a matrix.
func clone(
	m [][]float64,
) [][]float64 {
	out := make([][]float64, len(m))
	for i, row := range m {
		out[i] = append([]float64(nil), row...)
	}

	return out
}
