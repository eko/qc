//go:build race

package htmlreport

// longTitleFrames shortens TestLongTitle under the race detector, which
// makes gzip and JSON about thirty times slower.
const longTitleFrames = 12 * 60 * 25
