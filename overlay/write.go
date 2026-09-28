package overlay

import (
	"bufio"
	"fmt"
	"io"
)

// gap separates the widgets, in canvas units.
const gap = 16

// dialInset places the camera dial's centre from the right edge of the
// frame panel.
const dialInset = 46

// Write writes the ASS script of the overlay of in to w. It fails with
// ErrNoFrames without a frame timeline, and with the error of w.
func Write(
	w io.Writer,
	in Input,
	opts Options,
) error {
	t, err := newTitle(in)
	if err != nil {
		return err
	}

	c := newCanvas(t.width, t.height)
	clk := newClock(t.pts, t.duration, t.offset)
	bw := bufio.NewWriter(w)

	writeHeader(bw, c, opts.font())
	t.writeWidgets(bw, clk, c, opts)

	if err := bw.Flush(); err != nil {
		return fmt.Errorf("write overlay: %w", err)
	}

	return nil
}

// writeWidgets writes the events of every widget: the frame panel in the
// top-left corner, the quality panel in the top-right one, badges between
// them, the timeline along the bottom and the cut flashes around the
// picture.
func (t *title) writeWidgets(
	w *bufio.Writer,
	clk clock,
	c canvas,
	opts Options,
) {
	left := panel{x: margin, y: margin, width: leftWidth, rows: t.leftRows(opts)}
	left.write(w, clk)

	if y, ok := left.rowTop(ItemMotion); ok {
		t.writeDial(w, clk, left.x+left.width-dialInset, y+rowHeight)
	}

	right := panel{x: c.width - margin - rightWidth, y: margin, width: rightWidth, rows: t.rightRows(opts)}
	flagsX, flagsY := margin, margin+padding

	if len(left.rows) > 0 {
		flagsX += leftWidth + gap
	}

	// A narrow (portrait) picture stacks the widgets down its left side.
	if narrow := c.width < 2*margin+leftWidth+gap+rightWidth; narrow && len(left.rows) > 0 {
		right.x, right.y = margin, left.y+left.height()+gap
		flagsX, flagsY = margin, right.y+padding

		if len(right.rows) > 0 {
			flagsY += right.height() + gap
		}
	}

	right.write(w, clk)

	if opts.has(ItemFlags) {
		t.writeFlags(w, clk, flagsX, flagsY, c.width-margin)
	}

	if opts.has(ItemTimeline) {
		newTimeline(c).write(w, clk, t)
	}

	if opts.has(ItemShots) {
		t.writeCuts(w, clk, c)
	}
}
