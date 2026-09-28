//go:build !arm64 || purego

package siti

// squaresRow computes the squared Sobel magnitudes of a row (see
// sobelSquares).
func squaresRow(
	above, row, below []byte,
	out []int32,
) int64 {
	return sobelSquares(above, row, below, out)
}

// rowSums returns the sums of the square roots of the scratch rows (see
// magnitudeSums).
func rowSums(
	sq *squares,
) [rowsPerPass]float64 {
	return magnitudeSums(sq, 0, [rowsPerPass]float64{})
}

// rowMoments returns the sum and the sum of squares of b - a (see
// diffMoments).
func rowMoments(
	a, b []byte,
) (sum, sumSq int64) {
	return diffMoments(a, b)
}
