//go:build arm64 && !purego

package frame

// addBytesNEON is the vector loop of addBytes (thumb_arm64.s), over a
// multiple of 16 samples.
//
//go:noescape
func addBytesNEON(dst *uint16, src *byte, n int)

// addBytesLanes is the vector width of addBytesNEON, in samples.
const addBytesLanes = 16

// addBytes adds src to the column sums of dst, sixteen samples at a time.
func addBytes(
	dst []uint16,
	src []byte,
) {
	n := len(src) - len(src)%addBytesLanes
	if n > 0 {
		dst = dst[:len(src)]
		addBytesNEON(&dst[0], &src[0], n)
	}

	addBytesGeneric(dst[n:], src[n:])
}
