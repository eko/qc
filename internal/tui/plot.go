package tui

import (
	"math"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Mark is a glyph drawn over the dots of a plot at a point: a rung, by its
// number in the ladder table.
type Mark struct {
	X, Y  float64
	Glyph rune
	Style lipgloss.Style
}

// Plot draws series as braille curves on a character grid, with a y-axis
// gutter, and optionally marks, guides on the labelled rows and an x axis
// with ticks.
type Plot struct {
	Series []Series
	Marks  []Mark
	// Width counts the gutter; Height is the number of rows of the plot,
	// the x axis not counted.
	Width, Height int
	XLog          bool
	// XLo and XHi bound x, and what lies outside is not drawn; the range
	// of the points when XHi is not above XLo.
	XLo, XHi float64
	// YLo and YHi bound y; points outside are not drawn.
	YLo, YHi float64
	// YStep labels y every step and draws a faint guide on those rows;
	// with zero, the top, middle and bottom rows are labelled.
	YStep  float64
	YLabel func(float64) string
	// XLabel labels the ticks of an axis drawn under the plot; no axis
	// when nil.
	XLabel func(float64) string
}

// xTickGap is the least number of columns between two x tick labels.
const xTickGap = 2

// Lines renders the plot, one string per row; nil when there is no room or
// nothing to draw.
func (p Plot) Lines() []string {
	cols := p.Width - gutter

	xLo, xHi := scaleX(p.XLo, p.XLog), scaleX(p.XHi, p.XLog)
	if p.XHi <= p.XLo {
		xLo, xHi = p.dataRange()
	}

	if cols <= 0 || math.IsInf(xLo, 1) {
		return nil
	}

	if xHi == xLo {
		xHi = xLo + 1
	}

	yLo, yHi := p.YLo, p.YHi
	if yHi <= yLo {
		yHi = yLo + 1
	}

	c := newCanvas(cols, p.Height)
	px := func(x float64) int {
		return int(math.Round((scaleX(x, p.XLog) - xLo) / (xHi - xLo) * float64(2*cols-2)))
	}
	py := func(y float64) int {
		return int(math.Round((yHi - y) / (yHi - yLo) * float64(4*p.Height-2)))
	}

	p.draw(c, px, py)

	labels := p.yLabels(func(y float64) int { return py(y) / 4 }, yLo, yHi)
	lines := make([]string, p.Height, p.Height+2)

	for r := range p.Height {
		text, labelled := labels[r]
		if p.YStep <= 0 {
			text, labelled = yAxisText(r, p.Height, yLo, yHi, p.YLabel)
		}

		// With labels every step, the rows between them are plain.
		tick := " ┤"
		if p.YStep > 0 && !labelled {
			tick = " │"
		}

		lines[r] = Subtle.Render(padLeft(text, gutter-2)+tick) + c.row(r, labelled && p.YStep > 0)
	}

	if p.XLabel != nil {
		lines = append(lines, p.xAxis(cols, func(x float64) int { return px(x) / 2 }, xLo, xHi)...)
	}

	return lines
}

// draw puts the series on the canvas, later ones over earlier ones, then
// the marks over them; px and py map a point to its dot.
func (p Plot) draw(
	c *canvas,
	px, py func(float64) int,
) {
	for i := range p.Series {
		s := &p.Series[i]

		if s.Line {
			for j := 1; j < len(s.Points); j++ {
				a, b := s.Points[j-1], s.Points[j]
				c.line(px(a[0]), py(a[1]), px(b[0]), py(b[1]), &s.Style)
			}
		}

		if s.NoDots {
			continue
		}

		for _, pt := range s.Points {
			c.point(px(pt[0]), py(pt[1]), &s.Style)
		}
	}

	for i := range p.Marks {
		m := &p.Marks[i]
		c.mark(px(m.X)/2, py(m.Y)/4, m.Glyph, &m.Style)
	}
}

// dataRange is the range of the scaled x of the points.
func (p Plot) dataRange() (lo, hi float64) {
	lo, hi = math.Inf(1), math.Inf(-1)

	for _, s := range p.Series {
		for _, pt := range s.Points {
			x := scaleX(pt[0], p.XLog)
			lo, hi = min(lo, x), max(hi, x)
		}
	}

	return lo, hi
}

// yLabels are the labels of the rows a multiple of YStep falls on.
func (p Plot) yLabels(
	row func(float64) int,
	lo, hi float64,
) map[int]string {
	labels := map[int]string{}
	if p.YStep <= 0 {
		return labels
	}

	for v := math.Ceil(lo/p.YStep) * p.YStep; v <= hi+p.YStep/1e6; v += p.YStep {
		if r := row(v); r >= 0 && r < p.Height {
			if _, taken := labels[r]; !taken {
				labels[r] = p.YLabel(v)
			}
		}
	}

	return labels
}

// yAxisText is the label of a row when the top, middle and bottom rows
// are labelled.
func yAxisText(
	row, height int,
	lo, hi float64,
	label func(float64) string,
) (string, bool) {
	switch row {
	case 0:
		return label(hi), true
	case height - 1:
		return label(lo), true
	case height / 2:
		return label((lo + hi) / 2), true
	}

	return "", false
}

// xAxis draws the axis line under the plot with a tick at each labelled
// value, and their labels under it, centred on their ticks; a label that
// would touch the previous one is left out with its tick.
func (p Plot) xAxis(
	cols int,
	col func(float64) int,
	lo, hi float64,
) []string {
	line := []rune(strings.Repeat("─", cols))
	text := []rune(strings.Repeat(" ", cols))
	free := 0

	for _, v := range ticks(lo, hi, p.XLog) {
		at, label := col(v), []rune(p.XLabel(v))
		start := min(max(at-len(label)/2, 0), cols-len(label))

		if at < 0 || at >= cols || start < free {
			continue
		}

		line[at] = '┬'
		copy(text[start:], label)
		free = start + len(label) + xTickGap
	}

	return []string{
		Subtle.Render(strings.Repeat(" ", gutter-1) + "└" + string(line)),
		Subtle.Render(strings.Repeat(" ", gutter) + string(text)),
	}
}

// ticks are the values an axis spanning the scaled range lo to hi is
// labelled at: 1, 2 and 5 times the powers of ten on a log scale (only
// the powers when the range spans many decades), multiples of a round
// step on a linear one.
func ticks(
	lo, hi float64,
	logScale bool,
) []float64 {
	var out []float64

	if !logScale {
		step := roundStep((hi - lo) / maxLinearTicks)
		for v := math.Ceil(lo/step) * step; v <= hi; v += step {
			out = append(out, v)
		}

		return out
	}

	mantissas := []float64{1, 2, 5}
	if (hi-lo)/math.Ln10 > manyDecades {
		mantissas = []float64{1}
	}

	for e := math.Floor(lo / math.Ln10); e <= math.Ceil(hi/math.Ln10); e++ {
		for _, m := range mantissas {
			if v := m * math.Pow(10, e); math.Log(v) >= lo && math.Log(v) <= hi {
				out = append(out, v)
			}
		}
	}

	return out
}

const (
	// maxLinearTicks is about the number of ticks of a linear axis.
	maxLinearTicks = 6
	// manyDecades is the span of a log axis past which only the powers
	// of ten are labelled.
	manyDecades = 3
)

// roundStep is the 1, 2 or 5 × 10^k step at or above raw.
func roundStep(
	raw float64,
) float64 {
	mag := math.Pow(10, math.Floor(math.Log10(math.Max(raw, 1e-9))))
	for _, m := range []float64{1, 2, 5} {
		if raw <= m*mag {
			return m * mag
		}
	}

	return 10 * mag
}

// tickBitrate writes a bitrate tick short: 500k, 1M, 1.5M.
func tickBitrate(
	bps float64,
) string {
	if bps >= 1e6 {
		return strconv.FormatFloat(math.Round(bps/1e5)/10, 'f', -1, 64) + "M"
	}

	return strconv.FormatFloat(math.Round(bps/1e3), 'f', -1, 64) + "k"
}
