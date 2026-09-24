package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Series is one set of points of an XY plot.
type Series struct {
	Points [][2]float64
	Style  lipgloss.Style
	// Line joins consecutive points (sorted by x).
	Line bool
}

// braille dot bits, indexed [y][x] inside a 2×4 cell.
var brailleBits = [4][2]rune{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

// canvas is a braille canvas: every character cell holds 2×4 dots, which
// draws smooth curves with single-width characters only.
type canvas struct {
	cols, rows int
	dots       [][]rune
	styles     [][]*lipgloss.Style
}

func newCanvas(
	cols, rows int,
) *canvas {
	c := &canvas{
		cols:   cols,
		rows:   rows,
		dots:   make([][]rune, rows),
		styles: make([][]*lipgloss.Style, rows),
	}
	for r := range rows {
		c.dots[r] = make([]rune, cols)
		c.styles[r] = make([]*lipgloss.Style, cols)
	}

	return c
}

// set lights the dot at pixel (x, y), y growing downwards.
func (c *canvas) set(
	x, y int,
	style *lipgloss.Style,
) {
	if x < 0 || y < 0 || x >= 2*c.cols || y >= 4*c.rows {
		return
	}

	col, row := x/2, y/4
	c.dots[row][col] |= brailleBits[y%4][x%2]
	c.styles[row][col] = style
}

// line draws a segment with Bresenham's algorithm.
func (c *canvas) line(
	x0, y0, x1, y1 int,
	style *lipgloss.Style,
) {
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := sign(x1-x0), sign(y1-y0)
	e := dx + dy

	for {
		c.set(x0, y0, style)

		if x0 == x1 && y0 == y1 {
			return
		}

		if e2 := 2 * e; e2 >= dy {
			e += dy
			x0 += sx
		} else {
			e += dx
			y0 += sy
		}
	}
}

// point draws a 2×2 dot marker.
func (c *canvas) point(
	x, y int,
	style *lipgloss.Style,
) {
	for dx := range 2 {
		for dy := range 2 {
			c.set(x+dx, y+dy, style)
		}
	}
}

// row renders one character row of the canvas.
func (c *canvas) row(
	r int,
) string {
	var b strings.Builder

	for col := range c.cols {
		if c.dots[r][col] == 0 {
			b.WriteRune(' ')

			continue
		}

		b.WriteString(c.styles[r][col].Render(string(0x2800 + c.dots[r][col])))
	}

	return b.String()
}

// XYPlot draws series on a width×height character grid with a y-axis gutter,
// using braille dots. Later series are drawn over earlier ones. xLog plots x
// on a log scale.
func XYPlot(
	series []Series,
	width, height int,
	xLog bool,
	yLo, yHi float64,
	yLabel func(float64) string,
) []string {
	cols := width - gutter

	xLo, xHi := math.Inf(1), math.Inf(-1)
	for _, s := range series {
		for _, p := range s.Points {
			x := scaleX(p[0], xLog)
			xLo, xHi = min(xLo, x), max(xHi, x)
		}
	}

	if cols <= 0 || math.IsInf(xLo, 1) {
		return nil
	}

	if xHi == xLo {
		xHi = xLo + 1
	}

	if yHi <= yLo {
		yHi = yLo + 1
	}

	c := newCanvas(cols, height)
	px := func(x float64) int {
		return int(math.Round((scaleX(x, xLog) - xLo) / (xHi - xLo) * float64(2*cols-2)))
	}
	py := func(y float64) int {
		return int(math.Round((yHi - math.Max(math.Min(y, yHi), yLo)) / (yHi - yLo) * float64(4*height-2)))
	}

	for i := range series {
		s := &series[i]

		if s.Line {
			for j := 1; j < len(s.Points); j++ {
				a, b := s.Points[j-1], s.Points[j]
				c.line(px(a[0]), py(a[1]), px(b[0]), py(b[1]), &s.Style)
			}
		}

		for _, p := range s.Points {
			c.point(px(p[0]), py(p[1]), &s.Style)
		}
	}

	lines := make([]string, height)
	for r := range height {
		lines[r] = yAxis(r, height, yLo, yHi, yLabel) + c.row(r)
	}

	return lines
}

// scaleX maps x to the plot's horizontal scale. Log scales clamp at a tiny
// positive value so a zero bitrate cannot produce -Inf.
func scaleX(
	x float64,
	logScale bool,
) float64 {
	if logScale {
		return math.Log(math.Max(x, 1e-9))
	}

	return x
}

func sign(
	x int,
) int {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	}

	return 0
}

func abs(
	x int,
) int {
	if x < 0 {
		return -x
	}

	return x
}
