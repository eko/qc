package svg

import (
	"fmt"
	"html/template"
	"math"
	"strings"
)

// Figures that are read as they are drawn: every value is written next to
// its mark, and the exact figures are in the mark's title, so they need
// nothing from the page script.

// Geometry of the bars, in SVG units.
const (
	barRow    = 26
	barHeight = 14
	barRadius = 4
	// barLabels is the room of the labels on the left of the bars, and
	// barValues that of the values on their right.
	barLabels = 280
	barValues = 64
	// barLabelRunes bounds a label: the whole of it is in the title.
	barLabelRunes = 40
	barAxis       = 22
	barTicks      = 5
)

// Bar is one bar of Bars.
type Bar struct {
	Label string
	Value float64
	// Text is the value as written at the end of the bar.
	Text  string
	Color string
	// Detail completes the label in the bar's title.
	Detail string
}

// Mark is a reference value of Bars, drawn across them and named.
type Mark struct {
	Value float64
	Label string
}

// Bars compares values by the length of horizontal bars, one per line,
// each named on its left and valued on its right.
type Bars struct {
	Title string
	Width int
	Bars  []Bar
	// Format writes the values of the axis.
	Format func(float64) string
	Marks  []Mark
}

// HTML renders the bars as a figure.
func (c Bars) HTML() template.HTML {
	var b strings.Builder

	top := 4.0
	if len(c.Marks) > 0 {
		top = 18
	}

	height := top + float64(len(c.Bars))*barRow + barAxis
	left, right := float64(barLabels), float64(c.Width-barValues)

	highest := 0.0
	for _, bar := range c.Bars {
		highest = max(highest, bar.Value)
	}

	for _, m := range c.Marks {
		highest = max(highest, m.Value)
	}

	// Bars all at zero still get an axis.
	highest = max(highest, minLogX)
	step := niceStep(highest, barTicks)
	highest = max(math.Ceil(highest/step)*step, step)
	x := func(v float64) float64 { return left + v/highest*(right-left) }

	figureHead(&b, c.Title)
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %.0f" class="chart-svg" role="img" aria-label="%s">`, c.Width, height, template.HTMLEscapeString(c.Title))

	bottom := top + float64(len(c.Bars))*barRow

	for v := 0.0; v <= highest+step/2; v += step {
		fmt.Fprintf(&b, `<line class="grid" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, x(v), x(v), top, bottom)
		fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, x(v), bottom+15, template.HTMLEscapeString(c.Format(v)))
	}

	for i, bar := range c.Bars {
		mid := top + (float64(i)+0.5)*barRow
		title := bar.Label + ": " + bar.Text

		if bar.Detail != "" {
			title += " · " + bar.Detail
		}

		fmt.Fprintf(&b, `<g><title>%s</title>`, template.HTMLEscapeString(title))
		fmt.Fprintf(&b, `<text class="bar-label" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, left-10, mid+4, template.HTMLEscapeString(shorten(bar.Label, barLabelRunes)))
		fmt.Fprintf(&b, `<path fill="%s" d="%s"/>`, bar.Color, barPath(left, mid-barHeight/2, x(bar.Value)-left, barHeight))
		fmt.Fprintf(&b, `<text class="bar-value" x="%.1f" y="%.1f">%s</text></g>`, x(bar.Value)+6, mid+4, template.HTMLEscapeString(bar.Text))
	}

	for _, m := range c.Marks {
		fmt.Fprintf(&b, `<line class="line guide" stroke="currentColor" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, x(m.Value), x(m.Value), top-4, bottom)
		fmt.Fprintf(&b, `<text class="guide-label" fill="currentColor" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, x(m.Value), top-7, template.HTMLEscapeString(m.Label))
	}

	fmt.Fprintf(&b, `<line class="baseline" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, left, left, top, bottom)
	b.WriteString(`</svg></figure>`)

	return template.HTML(b.String()) //nolint:gosec // built from numbers and escaped labels
}

// barPath is a bar starting on its baseline at x, its far end rounded.
func barPath(
	x, y, width, height float64,
) string {
	r := min(barRadius, width, height/2)

	return fmt.Sprintf("M%.1f %.1fh%.1fa%.1f %.1f 0 0 1 %.1f %.1fv%.1fa%.1f %.1f 0 0 1 %.1f %.1fH%.1fz",
		x, y, width-r, r, r, r, r, height-2*r, r, r, -r, r, x)
}

// shorten cuts s to at most n runes, the last an ellipsis.
func shorten(
	s string,
	n int,
) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}

	return string(runes[:n-1]) + "…"
}

// figureHead opens a figure read without script and writes its title.
func figureHead(
	b *strings.Builder,
	title string,
) {
	b.WriteString(`<figure class="figure">`)

	if title != "" {
		fmt.Fprintf(b, `<div class="chart-head"><figcaption class="chart-title">%s</figcaption></div>`, template.HTMLEscapeString(title))
	}
}

// Geometry of a heatmap, in SVG units.
const (
	heatRow    = 30
	heatLabels = 56
	heatHead   = 20
	heatFoot   = 20
	heatGap    = 2
	// heatFaint is the share of the colour of a cell of value zero: a
	// cell is never mistaken for the page.
	heatFaint = 8
)

// Heatmap is a grid of values, each cell tinted by its value on one hue
// and holding it in figures.
type Heatmap struct {
	Title string
	Width int
	// Rows and Cols name the lines and columns; ColTitle names what the
	// columns are, under them.
	Rows, Cols []string
	ColTitle   string
	// Values are the cells, a line after the other, and Text what each
	// holds.
	Values [][]float64
	Text   [][]string
	// Max is the value of the full colour; the highest value when zero.
	Max   float64
	Color string
}

// HTML renders the heatmap as a figure.
func (c Heatmap) HTML() template.HTML {
	var b strings.Builder

	highest := c.Max
	if highest <= 0 {
		for _, line := range c.Values {
			for _, v := range line {
				highest = max(highest, v)
			}
		}
	}

	highest = max(highest, minLogX)
	height := heatHead + len(c.Rows)*heatRow + heatFoot
	cell := float64(c.Width-heatLabels) / float64(max(len(c.Cols), 1))

	figureHead(&b, c.Title)
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" class="chart-svg" role="img" aria-label="%s">`, c.Width, height, template.HTMLEscapeString(c.Title))

	for i, col := range c.Cols {
		fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="13" text-anchor="middle">%s</text>`, heatLabels+(float64(i)+0.5)*cell, template.HTMLEscapeString(col))
	}

	for j, row := range c.Rows {
		y := float64(heatHead + j*heatRow)
		fmt.Fprintf(&b, `<text class="axis" x="%d" y="%.1f" text-anchor="end">%s</text>`, heatLabels-8, y+heatRow/2+4, template.HTMLEscapeString(row))

		for i := range c.Cols {
			if j >= len(c.Values) || i >= len(c.Values[j]) {
				continue
			}

			share := heatFaint + (100-heatFaint)*min(c.Values[j][i]/highest, 1)
			x := heatLabels + float64(i)*cell

			fmt.Fprintf(&b, `<g><title>%s · %s %s: %s</title>`, template.HTMLEscapeString(row), template.HTMLEscapeString(c.ColTitle),
				template.HTMLEscapeString(c.Cols[i]), template.HTMLEscapeString(c.Text[j][i]))
			fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%d" rx="3" fill="color-mix(in oklab, %s %.0f%%, var(--surface-3))"/>`,
				x+heatGap/2, y+heatGap/2, cell-heatGap, heatRow-heatGap, c.Color, share)
			fmt.Fprintf(&b, `<text class="cell" x="%.1f" y="%.1f" text-anchor="middle">%s</text></g>`, x+cell/2, y+heatRow/2+4, template.HTMLEscapeString(c.Text[j][i]))
		}
	}

	fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%d" text-anchor="middle">%s</text>`, heatLabels+float64(c.Width-heatLabels)/2, height-5, template.HTMLEscapeString(c.ColTitle))
	b.WriteString(`</svg></figure>`)

	return template.HTML(b.String()) //nolint:gosec // built from numbers and escaped labels
}

// Geometry of the strips, in SVG units.
const (
	stripRow    = 28
	stripLabels = 190
	stripBounds = 70
	stripDot    = 4.5
)

// Strip is one line of Strips: the values of one measure, one per item.
type Strip struct {
	Label  string
	Values []float64
	// Names name the items, in the order of Values.
	Names []string
	// Format writes a value.
	Format func(float64) string
}

// Strips places the items of a set on one line per measure, each line on
// its own scale from its lowest to its highest value: where the items
// gather and where none is shows at a glance.
type Strips struct {
	Title string
	Width int
	Rows  []Strip
}

// HTML renders the strips as a figure.
func (c Strips) HTML() template.HTML {
	var b strings.Builder

	height := len(c.Rows)*stripRow + 4
	left, right := float64(stripLabels+stripBounds), float64(c.Width-stripBounds)

	figureHead(&b, c.Title)
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" class="chart-svg" role="img" aria-label="%s">`, c.Width, height, template.HTMLEscapeString(c.Title))

	for j, row := range c.Rows {
		if len(row.Values) == 0 {
			continue
		}

		y := (float64(j) + 0.5) * stripRow
		lo, hi := row.Values[0], row.Values[0]

		for _, v := range row.Values {
			lo, hi = min(lo, v), max(hi, v)
		}

		span := max(hi-lo, minLogX)

		fmt.Fprintf(&b, `<text class="bar-label" x="%d" y="%.1f">%s</text>`, 0, y+4, template.HTMLEscapeString(row.Label))
		fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, left-12, y+4, template.HTMLEscapeString(row.Format(lo)))
		fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f">%s</text>`, right+12, y+4, template.HTMLEscapeString(row.Format(hi)))
		fmt.Fprintf(&b, `<line class="baseline" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, left, right, y, y)

		for i, v := range row.Values {
			name := ""
			if i < len(row.Names) {
				name = row.Names[i] + ": "
			}

			fmt.Fprintf(&b, `<circle class="strip-dot" cx="%.1f" cy="%.1f" r="%.1f"><title>%s%s</title></circle>`,
				left+(v-lo)/span*(right-left), y, stripDot, template.HTMLEscapeString(name), template.HTMLEscapeString(row.Format(v)))
		}
	}

	b.WriteString(`</svg></figure>`)

	return template.HTML(b.String()) //nolint:gosec // built from numbers and escaped labels
}
