package overlay

import (
	"bufio"
	"fmt"
	"math"
	"strings"
)

// Geometry of the timeline, in canvas units: its header (series name and
// scale, file name), the height of its bars, the bin width (a bar every
// few units keeps the drawing small on long titles), the shot ticks and
// the playhead.
const (
	timelineHeader = 30
	barsHeight     = 44
	binWidth       = 3
	tickHeight     = 8
	playheadWidth  = 3
)

// vmafFloor is the lowest bottom of the VMAF scale: a timeline starting at
// 0 would squash the differences between good scores.
const vmafFloor = 80

// series is what the timeline charts: a value per bin (NaN for bins
// without one), between lo and hi.
type series struct {
	name   string
	values []float64
	lo, hi float64
	scale  string
}

// timeline is the chart of the title at the bottom of the picture.
type timeline struct {
	x, y, width int
}

// height is the height of the timeline box.
func (tl timeline) height() int {
	return timelineHeader + barsHeight + 2*padding
}

// newTimeline places the timeline along the bottom of the canvas.
func newTimeline(
	c canvas,
) timeline {
	tl := timeline{x: margin, width: c.width - 2*margin}
	tl.y = c.height - margin - tl.height()

	return tl
}

// write draws the timeline for the whole title: box, header, the series
// dim and lit up to the playhead (a clip animated over the title), shot
// ticks and the playhead (moving over the title). One event each, however
// long the title: libass animates them.
func (tl timeline) write(
	w *bufio.Writer,
	clk clock,
	t *title,
) {
	s := t.chartSeries(tl.width / binWidth)
	bx, by := tl.x+padding, tl.y+padding+timelineHeader
	bw := tl.width - 2*padding

	writeStatic(w, clk, layerPanel, shape(tl.x, tl.y, colourBlack, alphaPanel, rect(0, 0, tl.width, tl.height())))

	header := fmt.Sprintf(`{%s\fs%d}%s%s  %s%s`, at(bx, tl.y+padding), fontCaption, colour(colourOrange), s.name, colour(colourMuted), s.scale)
	writeStatic(w, clk, layerContent, header)

	if t.name != "" {
		name := fmt.Sprintf(`{\an9\pos(%d,%d)\fs%d}%s%s`, bx+bw, tl.y+padding, fontCaption, colour(colourMuted), escape(t.name))
		writeStatic(w, clk, layerContent, name)
	}

	bars := s.bars(bw, barsHeight)
	if bars == "" {
		return
	}

	// The title's frames span [offset, offset + duration] on the filter's
	// timeline, where the playhead and the lit part move from left to right.
	from := t.offset.Std().Milliseconds()
	to := from + t.duration.Std().Milliseconds()
	writeStatic(w, clk, layerContent, shape(bx, by, colourMuted, alphaDim, bars))

	lit := fmt.Sprintf(`{%s\bord0\shad0\1c%s\clip(%d,%d,%d,%d)\t(%d,%d,\clip(%d,%d,%d,%d))\p1}%s{\p0}`,
		at(bx, by), colourOrange, bx, by, bx, by+barsHeight, from, to, bx, by, bx+bw, by+barsHeight, bars)
	writeStatic(w, clk, layerContent, lit)

	if ticks := t.shotTicks(bw); ticks != "" {
		writeStatic(w, clk, layerTop, shape(bx, by-tickHeight-2, colourWhite, alphaOpaque, ticks))
	}

	playhead := fmt.Sprintf(`{\an7\move(%d,%d,%d,%d,%d,%d)\bord0\shad0\1c%s\p1}%s{\p0}`,
		bx, by-tickHeight, bx+bw, by-tickHeight, from, to, colourWhite, rect(-playheadWidth/2, 0, playheadWidth, barsHeight+tickHeight))
	writeStatic(w, clk, layerTop, playhead)
}

// bars is the drawing path of the series as bars from the bottom of a
// width×height box, "" when no bin has a value.
func (s series) bars(
	width, height int,
) string {
	var b strings.Builder

	span := max(s.hi-s.lo, 1e-9)

	for k, v := range s.values {
		if math.IsNaN(v) {
			continue
		}

		x0 := k * width / len(s.values)
		x1 := (k + 1) * width / len(s.values)
		h := int(math.Round(math.Min(math.Max((v-s.lo)/span, 0), 1) * float64(height)))
		// A bin at the bottom of the scale keeps a visible sliver.
		h = max(h, 1)

		b.WriteString(rect(x0, height-h, x1-x0, h))
	}

	return b.String()
}

// shotTicks is the drawing path of a tick at each shot cut, along a
// timeline width wide.
func (t *title) shotTicks(
	width int,
) string {
	if t.video == nil || t.duration <= 0 {
		return ""
	}

	var b strings.Builder

	for _, shot := range t.video.Shots[min(1, len(t.video.Shots)):] {
		x := int(math.Round(shot.Start.Seconds() / t.duration.Seconds() * float64(width)))
		b.WriteString(rect(x-1, 0, 2, tickHeight))
	}

	return b.String()
}
