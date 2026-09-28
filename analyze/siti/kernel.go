package siti

import (
	"math"
	"sync"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/stats"
)

// rowsPerPass is how many rows SpatialInformation measures together. The
// sum of the magnitudes of a row is a serial chain of floating-point
// additions, kept in pixel order so that results do not depend on the
// implementation; interleaving the chains of several rows hides the latency
// of each addition (and of the square roots) without reordering any of them.
const rowsPerPass = 4

// squares holds the squared Sobel magnitudes of rowsPerPass rows.
type squares [rowsPerPass][]int32

// squaresPool recycles the scratch rows of SpatialInformation, which runs
// once per frame on every SI/TI worker.
var squaresPool sync.Pool

// getSquares returns scratch rows of n samples.
func getSquares(
	n int,
) *squares {
	if s, ok := squaresPool.Get().(*squares); ok && len(s[0]) == n {
		return s
	}

	s := &squares{}
	for i := range s {
		s[i] = make([]int32, n)
	}

	return s
}

// SpatialInformation returns the standard deviation of the Sobel magnitude.
func SpatialInformation(
	p *frame.Plane,
) float64 {
	if p.Width < 3 || p.Height < 3 {
		return 0
	}

	sq := getSquares(p.Width - 2)
	defer squaresPool.Put(sq)

	var (
		sum   float64
		sumSq int64
	)

	for y := 1; y < p.Height-1; y += rowsPerPass {
		rows := min(rowsPerPass, p.Height-1-y)
		for i := range rows {
			sumSq += squaresRow(p.Row(y+i-1), p.Row(y+i), p.Row(y+i+1), sq[i])
		}

		// Rows are added in order, each sum being computed pixel by pixel
		// as a single loop would: the result is the same to the last bit.
		sums := rowSums(sq)
		for _, rowSum := range sums[:rows] {
			sum += rowSum
		}
	}

	n := float64((p.Width - 2) * (p.Height - 2))

	return stats.StdDev(sum, float64(sumSq), n)
}

// sobelSquares writes gx² + gy² for the inner pixels of a row to out
// (len(row)-2 values) and returns their sum. The 3×3 kernels are separable:
// gx is the difference of the vertically smoothed columns two apart (v),
// gy the horizontally smoothed vertical difference (d), so each pixel
// only reads the three samples of its rightmost column.
func sobelSquares(
	above, row, below []byte,
	out []int32,
) int64 {
	n := len(out)
	above, row, below = above[:n+2], row[:n+2], below[:n+2]

	v0 := int32(above[0]) + 2*int32(row[0]) + int32(below[0])
	d0 := int32(below[0]) - int32(above[0])
	v1 := int32(above[1]) + 2*int32(row[1]) + int32(below[1])
	d1 := int32(below[1]) - int32(above[1])

	var total int64

	for x := 2; x < len(above); x++ {
		a, r, b := int32(above[x]), int32(row[x]), int32(below[x])
		v2, d2 := a+2*r+b, b-a

		gx, gy := v2-v0, d0+2*d1+d2
		sq := gx*gx + gy*gy
		out[x-2] = sq
		total += int64(sq)

		v0, v1, d0, d1 = v1, v2, d1, d2
	}

	return total
}

// magnitudeSums adds to sums, for each scratch row, the square roots of
// its values from x on, in order.
func magnitudeSums(
	sq *squares,
	from int,
	sums [rowsPerPass]float64,
) [rowsPerPass]float64 {
	q0, q1, q2, q3 := sq[0][from:], sq[1][from:], sq[2][from:], sq[3][from:]
	q1, q2, q3 = q1[:len(q0)], q2[:len(q0)], q3[:len(q0)]

	s0, s1, s2, s3 := sums[0], sums[1], sums[2], sums[3]

	for x, v := range q0 {
		s0 += math.Sqrt(float64(v))
		s1 += math.Sqrt(float64(q1[x]))
		s2 += math.Sqrt(float64(q2[x]))
		s3 += math.Sqrt(float64(q3[x]))
	}

	return [rowsPerPass]float64{s0, s1, s2, s3}
}

// TemporalInformation returns the standard deviation of cur - prev.
func TemporalInformation(
	prev, cur *frame.Plane,
) float64 {
	var sum, sumSq int64

	for y := range cur.Height {
		b := cur.Row(y)
		s, q := rowMoments(prev.Row(y)[:len(b)], b)
		sum += s
		sumSq += q
	}

	return stats.StdDev(float64(sum), float64(sumSq), float64(cur.Width*cur.Height))
}

// diffMoments returns the sum and the sum of squares of b - a. Integer sums
// are exact in any order: four accumulators each break the dependency
// chains.
func diffMoments(
	a, b []byte,
) (sum, sumSq int64) {
	var s0, s1, s2, s3, q0, q1, q2, q3 int64

	x := 0
	for ; x+4 <= len(b); x += 4 {
		aa, bb := a[x:x+4:x+4], b[x:x+4:x+4]
		d0, d1 := int64(bb[0])-int64(aa[0]), int64(bb[1])-int64(aa[1])
		d2, d3 := int64(bb[2])-int64(aa[2]), int64(bb[3])-int64(aa[3])
		s0, s1, s2, s3 = s0+d0, s1+d1, s2+d2, s3+d3
		q0, q1, q2, q3 = q0+d0*d0, q1+d1*d1, q2+d2*d2, q3+d3*d3
	}

	for ; x < len(b); x++ {
		d := int64(b[x]) - int64(a[x])
		s0 += d
		q0 += d * d
	}

	return s0 + s1 + s2 + s3, q0 + q1 + q2 + q3
}
