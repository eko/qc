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
	// NoDots leaves out the marker of each point: a line drawn through
	// many samples stays one dot thick.
	NoDots bool
}

// braille dot bits, indexed [y][x] inside a 2×4 cell.
var brailleBits = [4][2]rune{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

// canvas is a braille canvas: every character cell holds 2×4 dots, which
// draws smooth curves with single-width characters only. A cell may hold
// a glyph instead, drawn over its dots.
type canvas struct {
	cols, rows int
	dots       [][]rune
	styles     [][]*lipgloss.Style
	glyphs     [][]rune
}

func newCanvas(
	cols, rows int,
) *canvas {
	c := &canvas{
		cols:   cols,
		rows:   rows,
		dots:   make([][]rune, rows),
		styles: make([][]*lipgloss.Style, rows),
		glyphs: make([][]rune, rows),
	}
	for r := range rows {
		c.dots[r] = make([]rune, cols)
		c.styles[r] = make([]*lipgloss.Style, cols)
		c.glyphs[r] = make([]rune, cols)
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

// mark draws glyph in the cell at col, row, over its dots.
func (c *canvas) mark(
	col, row int,
	glyph rune,
	style *lipgloss.Style,
) {
	if col < 0 || row < 0 || col >= c.cols || row >= c.rows {
		return
	}

	c.glyphs[row][col] = glyph
	c.styles[row][col] = style
}

// guideDot fills one empty cell in guideEvery of a guide row: enough to
// follow a level across the plot, faint enough to leave the curves first.
const (
	guideDot   = '·'
	guideEvery = 4
)

// row renders one character row of the canvas; on a guide row, faint
// dots fill some of the empty cells.
func (c *canvas) row(
	r int,
	guide bool,
) string {
	var b strings.Builder

	for col := range c.cols {
		switch {
		case c.glyphs[r][col] != 0:
			b.WriteString(c.styles[r][col].Render(string(c.glyphs[r][col])))

			continue
		case c.dots[r][col] == 0 && guide && col%guideEvery == guideEvery-1:
			b.WriteString(Subtle.Render(string(guideDot)))

			continue
		case c.dots[r][col] == 0:
			b.WriteRune(' ')

			continue
		}

		b.WriteString(c.styles[r][col].Render(string(0x2800 + c.dots[r][col])))
	}

	return b.String()
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
