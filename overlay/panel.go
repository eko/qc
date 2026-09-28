package overlay

import (
	"bufio"
	"math"
)

// canvasHeight is the height of the script's canvas: sizes and positions
// are written for a 1080-line picture and libass scales them to the video.
const canvasHeight = 1080

// fallbackAspect is the aspect ratio of the canvas when the video's size is
// unknown.
const fallbackAspect = 16.0 / 9

// Spacing of the layout, in canvas units.
const (
	margin      = 24
	padding     = 14
	rowHeight   = 34
	bigRow      = 46
	scoreRow    = 66
	captionRow  = 28
	leftWidth   = 500
	rightWidth  = 380
	panelAccent = 4
)

// canvas is the resolution of the script.
type canvas struct {
	width, height int
}

// newCanvas is a canvasHeight-line canvas with the aspect ratio of a
// width×height video.
func newCanvas(
	width, height int,
) canvas {
	aspect := fallbackAspect
	if width > 0 && height > 0 {
		aspect = float64(width) / float64(height)
	}

	return canvas{width: int(math.Round(canvasHeight * aspect)), height: canvasHeight}
}

// row is a line of a panel: text returns what it shows on frame i.
type row struct {
	height int
	text   func(i int) string
	// item is the item the row belongs to.
	item Item
}

// panel is a translucent box of rows, with an orange accent on its left.
type panel struct {
	x, y, width int
	rows        []row
}

// height is the height of the box around the rows.
func (p panel) height() int {
	h := 2 * padding
	for _, r := range p.rows {
		h += r.height
	}

	return h
}

// rowTop is the y of the first row of item, if the panel has one.
func (p panel) rowTop(
	item Item,
) (int, bool) {
	y := p.y + padding

	for _, r := range p.rows {
		if r.item == item {
			return y, true
		}

		y += r.height
	}

	return 0, false
}

// write writes the box, for the whole title, and one track per row.
func (p panel) write(
	w *bufio.Writer,
	clk clock,
) {
	if len(p.rows) == 0 {
		return
	}

	h := p.height()
	// One drawing per event: libass lays drawings of one event out side by
	// side, like glyphs.
	writeStatic(w, clk, layerPanel, shape(p.x, p.y, colourBlack, alphaPanel, rect(0, 0, p.width, h)))
	writeStatic(w, clk, layerPanel, shape(p.x, p.y, colourOrange, alphaOpaque, rect(0, 0, panelAccent, h)))

	y := p.y + padding

	for _, r := range p.rows {
		prefix := "{" + at(p.x+padding+panelAccent, y) + "}"
		text := r.text

		writeTrack(w, clk, track{layer: layerContent, text: func(i int) string {
			if s := text(i); s != "" {
				return prefix + s
			}

			return ""
		}})

		y += r.height
	}
}
