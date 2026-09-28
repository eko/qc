//go:build !arm64 || purego

package frame

// addBytes adds src to the column sums of dst.
func addBytes(
	dst []uint16,
	src []byte,
) {
	addBytesGeneric(dst, src)
}
