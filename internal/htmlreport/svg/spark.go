package svg

import (
	"fmt"
	"html/template"
	"math"
	"strings"
)

// Sparkline geometry, in SVG units: the page stretches it to its box.
const (
	sparkWidth  = 120
	sparkHeight = 28
	// sparkPoints bounds the points of a sparkline: a tile is a few dozen
	// pixels wide.
	sparkPoints = 96
	// sparkInset keeps the stroke inside the box at the extremes.
	sparkInset = 2
)

// Sparkline draws the trend of values as a small line with a faint area
// under it, for the key numbers of a report: no axis, no label, the shape
// alone. It draws in currentColor so the page tints it; it is empty when
// fewer than two values are finite.
func Sparkline(
	values []float64,
) template.HTML {
	var xs, ys []float64

	for i, v := range values {
		if isFinite(v) {
			xs, ys = append(xs, float64(i)), append(ys, v)
		}
	}

	if len(ys) < 2 {
		return ""
	}

	points := Downsample(xs, ys, sparkPoints)
	lo, hi := math.Inf(1), math.Inf(-1)

	for _, p := range points {
		lo, hi = math.Min(lo, p[1]), math.Max(hi, p[1])
	}

	if hi == lo {
		lo, hi = lo-1, hi+1
	}

	x0, x1 := points[0][0], points[len(points)-1][0]

	var line strings.Builder

	for i, p := range points {
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}

		x := (p[0] - x0) / (x1 - x0) * sparkWidth
		y := sparkInset + (hi-p[1])/(hi-lo)*(sparkHeight-2*sparkInset)
		fmt.Fprintf(&line, "%s%.1f,%.1f", cmd, x, y)
	}

	path := line.String()

	return template.HTML(fmt.Sprintf( //nolint:gosec // numbers only
		`<svg class="spark" viewBox="0 0 %d %d" preserveAspectRatio="none" aria-hidden="true">`+
			`<path class="spark-area" d="%sL%d,%dL0,%dZ"/><path class="spark-line" d="%s"/></svg>`,
		sparkWidth, sparkHeight, path, sparkWidth, sparkHeight, sparkHeight, path))
}
