//go:build arm64 && !purego

package siti

// sobelSquaresNEON and magnitudeSumsNEON are the vector loops of
// squaresRow and rowSums (kernel_arm64.s), over a multiple of their
// vector width; the remaining pixels go through the portable code, which
// computes the same values.
//
//go:noescape
func sobelSquaresNEON(above, row, below *byte, out *int32, n int) int64

//go:noescape
func magnitudeSumsNEON(q0, q1, q2, q3 *int32, n int, sums *[rowsPerPass]float64)

//go:noescape
func diffMomentsNEON(a, b *byte, n int) (sum, sumSq int64)

// Vector widths of the NEON loops, in pixels.
const (
	sobelLanes  = 16
	sumLanes    = 4
	momentLanes = 16
	// maxMomentSamples bounds one diffMomentsNEON call, whose 32-bit
	// accumulators of squares would overflow on longer runs.
	maxMomentSamples = 1 << 16
)

// squaresRow is sobelSquares, sixteen pixels at a time.
func squaresRow(
	above, row, below []byte,
	out []int32,
) int64 {
	n := len(out) - len(out)%sobelLanes
	if n == 0 {
		return sobelSquares(above, row, below, out)
	}

	// The loop reads up to sample n+1 of each row: a row has len(out)+2.
	above, row, below = above[:len(out)+2], row[:len(out)+2], below[:len(out)+2]
	total := sobelSquaresNEON(&above[0], &row[0], &below[0], &out[0], n)

	return total + sobelSquares(above[n:], row[n:], below[n:], out[n:])
}

// rowSums is magnitudeSums, four pixels of each row at a time: every row
// sums its pixels in order, as the portable loop does.
func rowSums(
	sq *squares,
) [rowsPerPass]float64 {
	var sums [rowsPerPass]float64

	n := len(sq[0]) - len(sq[0])%sumLanes
	if n > 0 {
		magnitudeSumsNEON(&sq[0][0], &sq[1][0], &sq[2][0], &sq[3][0], n, &sums)
	}

	return magnitudeSums(sq, n, sums)
}

// rowMoments is diffMoments, sixteen samples at a time.
func rowMoments(
	a, b []byte,
) (sum, sumSq int64) {
	a = a[:len(b)]

	for len(b) >= momentLanes {
		n := min(len(b)-len(b)%momentLanes, maxMomentSamples)
		s, q := diffMomentsNEON(&a[0], &b[0], n)
		sum, sumSq = sum+s, sumSq+q
		a, b = a[n:], b[n:]
	}

	s, q := diffMoments(a, b)

	return sum + s, sumSq + q
}
