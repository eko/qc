//go:build arm64 && !purego

package xpsnr

// sseNEON, highpassNEON, firstOrderNEON and secondOrderNEON are the vector
// loops of the row kernels of 8-bit samples (kernel_arm64.s), over a
// multiple of kernelLanes samples, and their 16NEON versions those of
// 16-bit samples (up to 12 bits), over a multiple of kernel16Lanes; the
// remaining samples go through the portable loops, which compute the same
// integers.
//
//go:noescape
func sseNEON(a, b *byte, n int) uint64

//go:noescape
func highpassNEON(up, cur, down *byte, n int) uint64

//go:noescape
func firstOrderNEON(cur *byte, p1 *int16, n int) uint64

//go:noescape
func secondOrderNEON(cur *byte, p1, p2 *int16, n int) uint64

//go:noescape
func sse16NEON(a, b *int16, n int) uint64

//go:noescape
func highpass16NEON(up, cur, down *int16, n int) uint64

//go:noescape
func firstOrder16NEON(cur, p1 *int16, n int) uint64

//go:noescape
func secondOrder16NEON(cur, p1, p2 *int16, n int) uint64

const (
	// kernelLanes and kernel16Lanes are the vector widths of the NEON
	// loops of 8-bit and 16-bit samples.
	kernelLanes   = 16
	kernel16Lanes = 8
	// maxKernelSamples bounds one call: the loops accumulate in 32-bit
	// lanes, at most 2·255² (8-bit squares) or 2·14·4095 (12-bit
	// high-pass) per lane and iteration.
	maxKernelSamples = 1 << 16
)

// lanes is the part of n samples the NEON loops of width width handle in
// one call.
func lanes(
	n, width int,
) int {
	return min(n-n%width, maxKernelSamples)
}

// sseRow8 is sseRow on 8-bit samples, sixteen at a time.
func sseRow8(
	a, b []uint8,
) uint64 {
	b = b[:len(a)]

	var sum uint64

	for n := lanes(len(a), kernelLanes); n > 0; n = lanes(len(a), kernelLanes) {
		sum += sseNEON(&a[0], &b[0], n)
		a, b = a[n:], b[n:]
	}

	return sum + sseRow(a, b)
}

// highpassRow8 is highpassRow on 8-bit samples, sixteen responses at a
// time: each reads the sample before and after it.
func highpassRow8(
	up, cur, down []uint8,
) uint64 {
	up, down = up[:len(cur)], down[:len(cur)]

	var sum uint64

	for n := lanes(len(cur)-2, kernelLanes); n > 0; n = lanes(len(cur)-2, kernelLanes) {
		sum += highpassNEON(&up[0], &cur[0], &down[0], n)
		up, cur, down = up[n:], cur[n:], down[n:]
	}

	return sum + highpassRow(up, cur, down)
}

// firstOrderRow8 is firstOrderRow on 8-bit samples, sixteen at a time.
func firstOrderRow8(
	cur []uint8,
	p1 []int16,
) uint64 {
	p1 = p1[:len(cur)]

	var sum uint64

	for n := lanes(len(cur), kernelLanes); n > 0; n = lanes(len(cur), kernelLanes) {
		sum += firstOrderNEON(&cur[0], &p1[0], n)
		cur, p1 = cur[n:], p1[n:]
	}

	return sum + firstOrderRow(cur, p1)
}

// secondOrderRow8 is secondOrderRow on 8-bit samples, sixteen at a time.
func secondOrderRow8(
	cur []uint8,
	p1, p2 []int16,
) uint64 {
	p1, p2 = p1[:len(cur)], p2[:len(cur)]

	var sum uint64

	for n := lanes(len(cur), kernelLanes); n > 0; n = lanes(len(cur), kernelLanes) {
		sum += secondOrderNEON(&cur[0], &p1[0], &p2[0], n)
		cur, p1, p2 = cur[n:], p1[n:], p2[n:]
	}

	return sum + secondOrderRow(cur, p1, p2)
}

// sseRow16 is sseRow on 16-bit samples, eight at a time.
func sseRow16(
	a, b []int16,
) uint64 {
	b = b[:len(a)]

	var sum uint64

	for n := lanes(len(a), kernel16Lanes); n > 0; n = lanes(len(a), kernel16Lanes) {
		sum += sse16NEON(&a[0], &b[0], n)
		a, b = a[n:], b[n:]
	}

	return sum + sseRow(a, b)
}

// highpassRow16 is highpassRow on 16-bit samples, eight responses at a
// time.
func highpassRow16(
	up, cur, down []int16,
) uint64 {
	up, down = up[:len(cur)], down[:len(cur)]

	var sum uint64

	for n := lanes(len(cur)-2, kernel16Lanes); n > 0; n = lanes(len(cur)-2, kernel16Lanes) {
		sum += highpass16NEON(&up[0], &cur[0], &down[0], n)
		up, cur, down = up[n:], cur[n:], down[n:]
	}

	return sum + highpassRow(up, cur, down)
}

// firstOrderRow16 is firstOrderRow on 16-bit samples, eight at a time.
func firstOrderRow16(
	cur, p1 []int16,
) uint64 {
	p1 = p1[:len(cur)]

	var sum uint64

	for n := lanes(len(cur), kernel16Lanes); n > 0; n = lanes(len(cur), kernel16Lanes) {
		sum += firstOrder16NEON(&cur[0], &p1[0], n)
		cur, p1 = cur[n:], p1[n:]
	}

	return sum + firstOrderRow(cur, p1)
}

// secondOrderRow16 is secondOrderRow on 16-bit samples, eight at a time.
func secondOrderRow16(
	cur, p1, p2 []int16,
) uint64 {
	p1, p2 = p1[:len(cur)], p2[:len(cur)]

	var sum uint64

	for n := lanes(len(cur), kernel16Lanes); n > 0; n = lanes(len(cur), kernel16Lanes) {
		sum += secondOrder16NEON(&cur[0], &p1[0], &p2[0], n)
		cur, p1, p2 = cur[n:], p1[n:], p2[n:]
	}

	return sum + secondOrderRow(cur, p1, p2)
}
