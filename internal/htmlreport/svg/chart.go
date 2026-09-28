// Package svg draws the charts of the HTML reports: a static SVG readable
// without script, a legend, and the chart's data at full resolution for the
// page script, which adds tooltips, zoom and legend toggles. It knows
// nothing of videos: series are numbers on time, bitrate, byte or plain
// axes.
package svg

import (
	"cmp"
	"fmt"
	"html/template"
	"math"
	"slices"
	"strings"
)

// Unit formats the values of an axis: on the server for the static chart,
// and in the page script for tooltips and zoomed axes.
type Unit string

// Units of chart axes.
const (
	UnitNumber  Unit = "number"
	UnitTime    Unit = "time"
	UnitBitrate Unit = "bitrate"
	UnitBytes   Unit = "bytes"
)

// Series is one line (or set of markers) of a chart.
type Series struct {
	Name string
	// Key groups the series toggled by one legend entry (a resolution's
	// curve and its probes); empty means Name.
	Key   string
	Color string
	// Points are drawn as is. When nil, they are the Samples downsampled:
	// a chart cannot show more points than it has pixels.
	Points [][2]float64
	// Samples are the measured values behind the drawing: tooltips and
	// zoomed views read them at full resolution.
	Samples *Samples
	// Area fills under the line; Markers draws points instead of a line.
	Area    bool
	Markers bool
	// Bold draws a thicker line.
	Bold bool
	// Step holds each value until the next point (stratum means): tooltips
	// read the value on the left of the cursor.
	Step bool
	// NoLegend hides the series from the legend.
	NoLegend bool
	// NoTip leaves the series out of tooltips (reference lines).
	NoTip bool
	// TipOnly lists the series in tooltips without drawing it (metrics of
	// the charted frames).
	TipOnly bool
	// Guide draws a reference line (a target, a ceiling, a mean) dashed and
	// labelled with its name at the right edge of the plot, instead of in
	// the legend: the label sits where the eye already is.
	Guide bool
	// Digits is the number of decimals of the values in tooltips.
	Digits int

	// envelope is the x, minimum and maximum of each drawn bucket, when
	// buckets average several samples: peaks stay visible behind the mean.
	envelope [][3]float64
}

// Samples are the measured values of a series.
type Samples struct {
	X, Y []float64
	// Frames are the frame numbers of the samples, when they are frames.
	Frames []int
	// Details describe each sample in point tooltips (ladder probes).
	Details []Detail
}

// Detail describes one sample in a point tooltip.
type Detail struct {
	Title  string
	Fields []Field
}

// Field is one labelled value of a Detail.
type Field struct {
	Label, Value string
}

// Chart describes a chart: a static SVG, progressively enhanced by the page
// script with tooltips, legend toggles and zoom.
type Chart struct {
	// Title names the chart above its legend.
	Title         string
	Width, Height int
	Series        []Series
	LogX          bool
	// LogY draws y on a log scale spanning the data, rounded out to 1, 2,
	// 5 × 10^k values (YMin and YMax are ignored): rungs a decade apart in
	// bitrate stay readable on one chart.
	LogY       bool
	YMin, YMax float64 // YMax == 0: auto
	X, Y       Unit
	// Nearest makes tooltips pick the point nearest to the pointer (scatter
	// plots) instead of every series at the pointer's x (time series).
	Nearest bool
	// MaxPoints bounds the drawn points of sampled series (0: default).
	MaxPoints int
	// Bands shade x ranges (e.g. black segments).
	Bands []Band
	// Zones shade y ranges (e.g. a loudness tolerance around its target).
	Zones []Zone
}

// Zone is a shaded y range, labelled at its top left.
type Zone struct {
	From, To float64
	Color    string
	Label    string
}

// Band is a shaded x range.
type Band struct {
	From, To float64
	Color    string
	Label    string
}

// Plot area margins, in SVG units: room for the y labels on the left and
// the x labels below.
const (
	padLeft   = 64
	padRight  = 16
	padTop    = 10
	padBottom = 28
)

const (
	// yTicks and xTicks are the approximate numbers of labelled values.
	yTicks = 4
	xTicks = 6
	// maxLogTicks drops the 2× ticks of a log axis spanning many decades.
	maxLogTicks = 9
	// minLogX clamps x on log scales, where zero has no position.
	minLogX = 1e-9
	// defaultMaxPoints bounds the drawn points of a sampled series.
	defaultMaxPoints = 800
	// edgeLabel is the distance to a plot edge under which an x label is
	// anchored on that edge instead of overflowing it.
	edgeLabel = 28
	// secondsPerHour switches time labels to h:mm:ss.
	secondsPerHour = 3600
	// timeOrigin is the share of a time axis under which it starts at 0.
	timeOrigin = 0.05
	// markerRadius makes markers 8 units wide.
	markerRadius = 4
	// guideLabelGap is the least vertical distance between two guide
	// labels, so close reference lines keep readable names.
	guideLabelGap = 12
	// guideLabelLift raises a guide label above its line.
	guideLabelLift = 5
)

// timeSteps are the x tick steps of time axes, in seconds.
var timeSteps = []float64{
	0.001, 0.002, 0.005, 0.01, 0.02, 0.05, 0.1, 0.2, 0.5,
	1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600,
}

// plot maps data coordinates to SVG coordinates.
type plot struct {
	chart              Chart
	xLo, xHi, yLo, yHi float64
	yStep              float64
	left, top, pw, ph  float64
	bottom, right      float64
	xLabelY            float64
}

func (c Chart) plot() plot {
	xLo, xHi, yLo, yHi, yStep := c.bounds()
	pw := float64(c.Width - padLeft - padRight)
	ph := float64(c.Height - padTop - padBottom)

	return plot{
		chart: c,
		xLo:   xLo, xHi: xHi, yLo: yLo, yHi: yHi, yStep: yStep,
		left: padLeft, top: padTop, pw: pw, ph: ph,
		right: padLeft + pw, bottom: padTop + ph, xLabelY: float64(c.Height - 8),
	}
}

// x maps a data x to an SVG x.
func (p plot) x(
	v float64,
) float64 {
	return p.left + (p.chart.scaleX(v)-p.xLo)/(p.xHi-p.xLo)*p.pw
}

// y maps a data y to an SVG y, clamped to the plot area.
func (p plot) y(
	v float64,
) float64 {
	v = math.Max(math.Min(v, p.yHi), p.yLo)

	if p.chart.LogY {
		return p.top + math.Log(p.yHi/v)/math.Log(p.yHi/p.yLo)*p.ph
	}

	return p.top + (p.yHi-v)/(p.yHi-p.yLo)*p.ph
}

// HTML renders the chart as a figure: its legend, the static SVG and the
// data the page script reads for tooltips and zoom.
func (c Chart) HTML() template.HTML {
	c = c.withPoints()
	p := c.plot()

	var b strings.Builder

	mode := "x"
	if c.Nearest {
		mode = "point"
	}

	fmt.Fprintf(&b, `<figure class="chart" data-mode="%s">`, mode)
	c.head(&b)
	b.WriteString(`<div class="plot">`)
	p.svg(&b)
	b.WriteString(`</div>`)
	writeData(&b, p.data())
	b.WriteString(`</figure>`)

	return template.HTML(b.String()) //nolint:gosec // built from numbers, JSON with escaped HTML and escaped labels
}

// SVG renders the static chart alone.
func (c Chart) SVG() template.HTML {
	c = c.withPoints()

	var b strings.Builder
	c.plot().svg(&b)

	return template.HTML(b.String()) //nolint:gosec // built from numbers and escaped labels
}

// withPoints fills the drawn points of sampled series.
func (c Chart) withPoints() Chart {
	limit := cmp.Or(c.MaxPoints, defaultMaxPoints)
	series := slices.Clone(c.Series)

	for i, s := range series {
		if s.Points == nil && s.Samples != nil && !s.TipOnly {
			series[i].Points = s.Samples.drawn(limit, s.Markers)
			if !s.Markers && !s.Area && min(len(s.Samples.X), len(s.Samples.Y)) > limit {
				series[i].envelope = envelope(s.Samples.X, s.Samples.Y, limit)
			}
		}
	}

	c.Series = series

	return c
}

func (p plot) svg(
	b *strings.Builder,
) {
	c := p.chart

	fmt.Fprintf(b, `<svg viewBox="0 0 %d %d" class="chart-svg" role="img" aria-label="%s">`,
		c.Width, c.Height, template.HTMLEscapeString(c.ariaLabel()))
	p.grid(b)
	p.zones(b)
	p.bands(b)
	p.xAxis(b)

	for i, s := range c.Series {
		if s.TipOnly {
			continue
		}

		fmt.Fprintf(b, `<g class="series" data-s="%d" data-key="%s">`, i, template.HTMLEscapeString(s.key()))
		p.series(b, s)
		b.WriteString(`</g>`)
	}

	p.guideLabels(b)
	b.WriteString(`</svg>`)
}

// zones shades the y ranges, each labelled at its top left.
func (p plot) zones(
	b *strings.Builder,
) {
	if len(p.chart.Zones) == 0 {
		return
	}

	b.WriteString(`<g class="zones">`)

	for _, z := range p.chart.Zones {
		top, bottom := p.y(math.Max(z.From, z.To)), p.y(math.Min(z.From, z.To))
		fmt.Fprintf(b, `<rect x="%.0f" y="%.1f" width="%.1f" height="%.1f" style="fill:%s" class="zone"><title>%s</title></rect>`,
			p.left, top, p.pw, math.Max(bottom-top, 1), template.HTMLEscapeString(z.Color), template.HTMLEscapeString(z.Label))
		fmt.Fprintf(b, `<text x="%.0f" y="%.1f" class="zone-label">%s</text>`, p.left+6, bottom+guideLabelGap-1, template.HTMLEscapeString(z.Label))
	}

	b.WriteString(`</g>`)
}

// guideLabels names the reference lines at the right edge of the plot,
// pushed apart when lines are close.
func (p plot) guideLabels(
	b *strings.Builder,
) {
	type label struct {
		y           float64
		name, color string
	}

	var list []label

	for _, s := range p.chart.Series {
		if s.Guide && len(s.Points) > 0 {
			list = append(list, label{math.Max(p.y(s.Points[0][1])-guideLabelLift, p.top+guideLabelGap-2), s.Name, s.Color})
		}
	}

	if len(list) == 0 {
		return
	}

	slices.SortStableFunc(list, func(a, b label) int { return cmp.Compare(a.y, b.y) })
	b.WriteString(`<g class="guides">`)

	for i, l := range list {
		if i > 0 && l.y-list[i-1].y < guideLabelGap {
			list[i].y = list[i-1].y + guideLabelGap
		}

		fmt.Fprintf(b, `<text x="%.1f" y="%.1f" text-anchor="end" style="fill:%s" class="guide-label">%s</text>`,
			p.right-4, list[i].y, template.HTMLEscapeString(l.color), template.HTMLEscapeString(l.name))
	}

	b.WriteString(`</g>`)
}

// ariaLabel names the chart after its series.
func (c Chart) ariaLabel() string {
	var names []string

	for _, s := range c.Series {
		if s.Name != "" && !s.NoLegend && !s.TipOnly && !s.Guide {
			names = append(names, s.Name)
		}
	}

	if c.Title != "" {
		return c.Title + ": " + strings.Join(names, ", ")
	}

	return "Chart: " + strings.Join(names, ", ")
}

func (s Series) key() string {
	return cmp.Or(s.Key, s.Name)
}

func (p plot) bands(
	b *strings.Builder,
) {
	b.WriteString(`<g class="bands">`)

	for _, band := range p.chart.Bands {
		x0, x1 := p.x(band.From), p.x(band.To)
		fmt.Fprintf(b, `<rect x="%.1f" y="%.0f" width="%.1f" height="%.1f" style="fill:%s" class="band"><title>%s</title></rect>`,
			x0, p.top, math.Max(x1-x0, 1), p.ph, template.HTMLEscapeString(band.Color), template.HTMLEscapeString(band.Label))
	}

	b.WriteString(`</g>`)
}

// grid draws the horizontal grid with the y labels.
func (p plot) grid(
	b *strings.Builder,
) {
	c := p.chart

	b.WriteString(`<g class="yaxis">`)

	ticks := linearTicks(p.yLo, p.yHi, p.yStep)
	if c.LogY {
		ticks = logTicks(p.yLo, p.yHi)
	}

	for _, v := range ticks {
		step := p.yStep
		if c.LogY {
			step = v
		}

		y := p.y(v)
		fmt.Fprintf(b, `<line x1="%.0f" x2="%.1f" y1="%.1f" y2="%.1f" class="grid"/>`, p.left, p.right, y, y)
		fmt.Fprintf(b, `<text x="%.0f" y="%.1f" class="axis" text-anchor="end">%s</text>`,
			p.left-8, y+4, template.HTMLEscapeString(c.Y.tick(v, step)))
	}

	fmt.Fprintf(b, `<line x1="%.0f" x2="%.1f" y1="%.1f" y2="%.1f" class="baseline"/>`, p.left, p.right, p.bottom, p.bottom)
	b.WriteString(`</g>`)
}

// xAxis draws the x labels and their ticks.
func (p plot) xAxis(
	b *strings.Builder,
) {
	c := p.chart

	b.WriteString(`<g class="xaxis">`)

	for _, t := range c.xTicks(p.xLo, p.xHi) {
		x := p.x(t.value)

		anchor := "middle"

		switch {
		case x-p.left < edgeLabel:
			anchor = "start"
		case p.right-x < edgeLabel:
			anchor = "end"
		}

		fmt.Fprintf(b, `<line x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f" class="tick"/>`, x, x, p.bottom, p.bottom+4)
		fmt.Fprintf(b, `<text x="%.1f" y="%.0f" class="axis" text-anchor="%s">%s</text>`,
			x, p.xLabelY, anchor, template.HTMLEscapeString(t.label))
	}

	b.WriteString(`</g>`)
}

type tick struct {
	value float64
	label string
}

// xTicks are the labelled x values between lo and hi, in scaled x.
func (c Chart) xTicks(
	lo, hi float64,
) []tick {
	var out []tick

	if c.LogX {
		for _, v := range logTicks(math.Exp(lo), math.Exp(hi)) {
			out = append(out, tick{v, c.X.tick(v, v)})
		}

		return out
	}

	step := niceStep(hi-lo, xTicks)
	if c.X == UnitTime {
		step = timeStep(hi - lo)
	}

	for _, v := range linearTicks(lo, hi, step) {
		label := c.X.tick(v, step)
		// Every label of an axis reaching an hour shows hours.
		if c.X == UnitTime && hi >= secondsPerHour {
			label = clockLabel(v, stepDigits(step), true)
		}

		out = append(out, tick{v, label})
	}

	return out
}

func (p plot) series(
	b *strings.Builder,
	s Series,
) {
	if len(s.Points) == 0 {
		return
	}

	if s.Markers {
		p.markers(b, s)

		return
	}

	var path strings.Builder

	for i, pt := range s.Points {
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}

		fmt.Fprintf(&path, "%s%.1f,%.1f ", cmd, p.x(pt[0]), p.y(pt[1]))
	}

	color := template.HTMLEscapeString(s.Color)

	if s.Area {
		first, last := s.Points[0], s.Points[len(s.Points)-1]
		fmt.Fprintf(b, `<path d="%sL%.1f,%.1f L%.1f,%.1f Z" style="fill:%s" class="area"/>`,
			path.String(), p.x(last[0]), p.bottom, p.x(first[0]), p.bottom, color)
	}

	if len(s.envelope) > 0 {
		var env strings.Builder

		for i, e := range s.envelope {
			cmd := "L"
			if i == 0 {
				cmd = "M"
			}

			fmt.Fprintf(&env, "%s%.1f,%.1f ", cmd, p.x(e[0]), p.y(e[2]))
		}

		for i := len(s.envelope) - 1; i >= 0; i-- {
			fmt.Fprintf(&env, "L%.1f,%.1f ", p.x(s.envelope[i][0]), p.y(s.envelope[i][1]))
		}

		fmt.Fprintf(b, `<path d="%sZ" style="fill:%s" class="envelope"/>`, env.String(), color)
	}

	class := s.lineClass()

	fmt.Fprintf(b, `<path d="%s" style="stroke:%s" class="%s"><title>%s</title></path>`,
		strings.TrimSpace(path.String()), color, class, template.HTMLEscapeString(s.Name))
}

// lineClass styles a line: dashed guides, bold headline series.
func (s Series) lineClass() string {
	switch {
	case s.Guide:
		return "line guide"
	case s.Bold:
		return "line bold"
	}

	return "line"
}

// markers draws a dot per point, with its values as a native tooltip for
// pages read without script.
func (p plot) markers(
	b *strings.Builder,
	s Series,
) {
	c := p.chart
	color := template.HTMLEscapeString(s.Color)

	for _, pt := range s.Points {
		tooltip := fmt.Sprintf("%s: %s, %s", s.Name, c.X.Format(pt[0]), c.Y.Format(pt[1]))
		fmt.Fprintf(b, `<circle cx="%.1f" cy="%.1f" r="%d" style="fill:%s" class="dot"><title>%s</title></circle>`,
			p.x(pt[0]), p.y(pt[1]), markerRadius, color, template.HTMLEscapeString(tooltip))
	}
}

// head writes the chart's title and legend.
func (c Chart) head(
	b *strings.Builder,
) {
	b.WriteString(`<div class="chart-head">`)

	if c.Title != "" {
		fmt.Fprintf(b, `<figcaption class="chart-title">%s</figcaption>`, template.HTMLEscapeString(c.Title))
	}

	c.legend(b)
	b.WriteString(`</div>`)
}

// legend lists the legend keys as toggle buttons; a single key names the
// chart's series.
func (c Chart) legend(
	b *strings.Builder,
) {
	type entry struct {
		key, name, color, shape string
	}

	var entries []entry

	for _, s := range c.Series {
		if s.Name == "" || s.NoLegend || s.TipOnly || s.Guide {
			continue
		}

		shape := "line"

		switch {
		case s.Markers:
			shape = "dot"
		case s.Area:
			shape = "area"
		}

		entries = append(entries, entry{s.key(), s.Name, s.Color, shape})
	}

	if len(entries) == 0 {
		return
	}

	b.WriteString(`<div class="legend">`)

	for _, e := range entries {
		fmt.Fprintf(b, `<button type="button" class="key" data-key="%s" aria-pressed="true"><i class="sw %s" style="--c:%s"></i>%s</button>`,
			template.HTMLEscapeString(e.key), e.shape, template.HTMLEscapeString(e.color), template.HTMLEscapeString(e.name))
	}

	b.WriteString(`</div>`)
}
