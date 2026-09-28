//go:build !arm64 || purego

package xpsnr

// The row loops of 8-bit and 16-bit samples are the portable ones.
var (
	sseRow8          = sseRow[uint8]
	highpassRow8     = highpassRow[uint8]
	firstOrderRow8   = firstOrderRow[uint8]
	secondOrderRow8  = secondOrderRow[uint8]
	sseRow16         = sseRow[int16]
	highpassRow16    = highpassRow[int16]
	firstOrderRow16  = firstOrderRow[int16]
	secondOrderRow16 = secondOrderRow[int16]
)
