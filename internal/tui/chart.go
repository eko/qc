package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/eko/qc/media"
)

// blocks are the eighths of a character cell, from empty to full.
var blocks = []rune(" ▁▂▃▄▅▆▇█")

// gutter is the width reserved on the left of every chart row for labels.
const gutter = 12

// Aggregation selects how samples falling in the same column are combined.
type Aggregation int

// Aggregations.
const (
	// Mean averages the samples of a column: the typical level.
	Mean Aggregation = iota
	// Max keeps the largest sample of a column, so peaks never disappear
	// when a long series is squeezed into a few columns.
	Max
)

// resample maps values onto width columns.
func resample(
	values []float64,
	width int,
	agg Aggregation,
) []float64 {
	if len(values) == 0 || width <= 0 {
		return nil
	}

	out := make([]float64, width)

	for col := range width {
		from := col * len(values) / width
		to := max(from+1, (col+1)*len(values)/width)
		out[col] = aggregate(values[from:to], agg)
	}

	return out
}

func aggregate(
	values []float64,
	agg Aggregation,
) float64 {
	if agg == Max {
		acc := math.Inf(-1)
		for _, v := range values {
			acc = max(acc, v)
		}

		return acc
	}

	acc := 0.0
	for _, v := range values {
		acc += v
	}

	return acc / float64(len(values))
}

// Scale is the value range of a chart. A zero Hi scales to the maximum value.
type Scale struct {
	Lo, Hi float64
}

// BarChart draws values as vertical bars height rows tall, with a y-axis on
// the left. The returned lines are ready to print, each width columns wide.
func BarChart(
	values []float64,
	width, height int,
	agg Aggregation,
	scale Scale,
	style lipgloss.Style,
	label func(float64) string,
) []string {
	cols := resample(values, width-gutter, agg)
	if len(cols) == 0 {
		return nil
	}

	lo, top := scale.Lo, scale.Hi
	if top == 0 {
		top = aggregate(cols, Max)
	}

	if top <= lo {
		top = lo + 1
	}

	steps := len(blocks) - 1
	levels := float64(height * steps)
	lines := make([]string, height)

	for row := range height {
		var b strings.Builder

		// Row 0 is the top of the chart: it only fills above its base.
		base := float64((height - 1 - row) * steps)

		for _, v := range cols {
			fill := (v - lo) / (top - lo) * levels
			idx := int(math.Round(min(max(fill-base, 0), float64(steps))))
			b.WriteRune(blocks[idx])
		}

		lines[row] = yAxis(row, height, lo, top, label) + style.Render(b.String())
	}

	return lines
}

// yAxis is the gutter of a chart row: the top, middle and bottom rows carry
// a value label.
func yAxis(
	row, height int,
	lo, hi float64,
	label func(float64) string,
) string {
	text := ""

	switch row {
	case 0:
		text = label(hi)
	case height - 1:
		text = label(lo)
	case height / 2:
		text = label((lo + hi) / 2)
	}

	return Subtle.Render(padLeft(text, gutter-2) + " ┤")
}

// Sparkline draws values on a single line, width columns wide. It scales to
// the range of the values: a flat series draws the lowest block.
func Sparkline(
	values []float64,
	width int,
	agg Aggregation,
	style lipgloss.Style,
) string {
	cols := resample(values, width, agg)

	lo, hi := math.Inf(1), math.Inf(-1)
	for _, v := range cols {
		lo, hi = min(lo, v), max(hi, v)
	}

	var b strings.Builder

	for _, v := range cols {
		// Index 0 is blank: the lowest value still draws a visible block.
		idx := 1
		if hi > lo {
			idx = 1 + int(math.Round((v-lo)/(hi-lo)*float64(len(blocks)-2)))
		}

		b.WriteRune(blocks[idx])
	}

	return style.Render(b.String())
}

// axisLine lays out start, middle and end labels under a chart width columns
// wide, aligned with its plot area.
func axisLine(
	start, mid, end string,
	width int,
) string {
	cols := width - gutter
	gap1 := max(1, cols/2-len(start)-len(mid)/2)
	gap2 := max(1, cols-len(start)-gap1-len(mid)-len(end))

	return Subtle.Render(strings.Repeat(" ", gutter) + start + strings.Repeat(" ", gap1) + mid + strings.Repeat(" ", gap2) + end)
}

// rowLabel is the gutter of a labelled chart row.
func rowLabel(
	s string,
) string {
	return Subtle.Render(padLeft(s, gutter-2) + "  ")
}

// padLeft right-aligns s, which may be styled, on width columns.
func padLeft(
	s string,
	width int,
) string {
	if n := lipgloss.Width(s); n < width {
		return strings.Repeat(" ", width-n) + s
	}

	return s
}

// timeColumn maps a time of a total-long timeline onto cols columns.
func timeColumn(
	total media.Duration,
	cols int,
) func(media.Duration) int {
	return func(d media.Duration) int {
		if total <= 0 {
			return 0
		}

		return min(cols-1, max(0, int(d.Seconds()/total.Seconds()*float64(cols))))
	}
}

// timeAxis labels the start, middle and end of a d-long timeline.
func timeAxis(
	d media.Duration,
	width int,
) string {
	return axisLine(Clock(0, true), Clock(d/2, true), Clock(d, true), width)
}
