package overlay

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/eko/qc/media"
)

// labelWidth aligns the values of the rows: labels are padded to it.
const labelWidth = 6

// label is a muted row label padded to labelWidth, followed by the value
// colour.
func label(
	s string,
) string {
	return colour(colourMuted) + fmt.Sprintf("%-*s", labelWidth, s) + colour(colourWhite)
}

// timecode formats d as hh:mm:ss.mmm.
func timecode(
	d media.Duration,
) string {
	ms := d.Std().Round(time.Millisecond).Milliseconds()

	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3_600_000, ms/60_000%60, ms/1000%60, ms%1000)
}

// Decimal multiples of the sizes and bitrates.
const (
	kilo = 1e3
	mega = 1e6
)

// byteSize formats a frame size: 812 B, 45.2 kB, 1.23 MB.
func byteSize(
	bytes int,
) string {
	b := float64(bytes)

	switch {
	case b < kilo:
		return strconv.Itoa(bytes) + " B"
	case b < mega:
		return decimals(b/kilo) + " kB"
	}

	return decimals(b/mega) + " MB"
}

// bitRate formats a bitrate: 845 kb/s, 4.82 Mb/s, 12.4 Mb/s.
func bitRate(
	bits float64,
) string {
	if bits < mega {
		return strconv.FormatFloat(math.Round(bits/kilo), 'f', 0, 64) + " kb/s"
	}

	return decimals(bits/mega) + " Mb/s"
}

// decimals formats v with three significant digits (two decimals under
// 10, one under 100, none above): enough to read, few enough to stay still.
func decimals(
	v float64,
) string {
	switch {
	case v < 10:
		return strconv.FormatFloat(v, 'f', 2, 64)
	case v < 100:
		return strconv.FormatFloat(v, 'f', 1, 64)
	}

	return strconv.FormatFloat(v, 'f', 0, 64)
}

// integer formats v rounded to an integer.
func integer(
	v float64,
) string {
	// Adding zero turns the -0 of small negative values into 0.
	return strconv.FormatFloat(math.Round(v)+0, 'f', 0, 64)
}

// signed formats v with one decimal and its sign (+0.0 for zero).
func signed(
	v float64,
) string {
	// Adding zero turns the -0 of small negative values into 0.
	r := math.Round(v*10)/10 + 0
	if r >= 0 {
		return "+" + strconv.FormatFloat(r, 'f', 1, 64)
	}

	return strconv.FormatFloat(r, 'f', 1, 64)
}
