package overlay

import (
	"bufio"
	"slices"

	"github.com/eko/qc/media"
)

// outOfRangeShare is the share of a frame's samples outside the nominal
// levels from which it is flagged: isolated overshoots of a sharp edge
// are common and harmless.
const outOfRangeShare = 0.01

// badgeSlot and badgeRow are the width and height of a badge's slot, in
// canvas units: the longest badge ("OUT OF RANGE") at fontBadge, with the
// box around it.
const (
	badgeSlot = 230
	badgeRow  = 44
)

// cutBorder is the width of the orange frame drawn around the picture on
// the first cutFlash frames of a shot.
const (
	cutBorder = 6
	cutFlash  = 2
)

// badge is a flag of the frame and the frames it is raised on.
type badge struct {
	name   string
	raised func(i int) bool
}

// badges are the flags the title can raise: the picture is letterboxed or
// pillarboxed, the frame is black, frozen, banded (CAMBI of a comparison)
// or out of the nominal levels.
func (t *title) badges() []badge {
	var out []badge

	// The badges of the whole title come first: they keep the first slots,
	// and the others do not move around them.
	if v := t.video; v != nil {
		out = append(out,
			badge{"LETTERBOX", func(int) bool { return v.Crop.Letterbox }},
			badge{"PILLARBOX", func(int) bool { return v.Crop.Pillarbox }},
			badge{"BLACK", func(i int) bool { return inSegments(v.Black.Segments, t.pts[i]) }},
			badge{"FROZEN", func(i int) bool { return inSegments(v.Freeze.Segments, t.pts[i]) }})
	}

	if q := t.quality; q != nil && q.Banding != nil {
		segments := make([]media.Interval, len(q.Banding.Segments))
		for j, s := range q.Banding.Segments {
			segments[j] = s.Interval
		}

		out = append(out, badge{"BANDING", func(i int) bool { return inSegments(segments, t.pts[i]) }})
	}

	if f := t.frames; f != nil && len(f.LumaMin) > 0 {
		out = append(out, badge{"OUT OF RANGE", t.outOfRange})
	}

	return out
}

// writeFlags writes the raised badges side by side from (x, y), in a fixed
// order, wrapping before right: slot k shows the k-th badge raised on the
// frame, so badges never leave gaps.
func (t *title) writeFlags(
	w *bufio.Writer,
	clk clock,
	x, y, right int,
) {
	badges := t.badges()
	raised := make([][]string, t.n())

	for i := range raised {
		for _, f := range badges {
			if f.raised(i) {
				raised[i] = append(raised[i], f.name)
			}
		}
	}

	slots := 0
	for _, names := range raised {
		slots = max(slots, len(names))
	}

	perRow := max((right-x)/badgeSlot, 1)

	for k := range slots {
		pos := "{" + at(x+k%perRow*badgeSlot, y+k/perRow*badgeRow) + "}"

		writeTrack(w, clk, track{layer: layerContent, style: styleBadge, text: func(i int) string {
			if k < len(raised[i]) {
				return pos + raised[i][k]
			}

			return ""
		}})
	}
}

// writeCuts draws an orange frame around the picture on the first frames
// of every shot but the first.
func (t *title) writeCuts(
	w *bufio.Writer,
	clk clock,
	c canvas,
) {
	if t.video == nil || len(t.video.Shots) < 2 {
		return
	}

	border := rect(0, 0, c.width, cutBorder) + rect(0, c.height-cutBorder, c.width, cutBorder) +
		rect(0, 0, cutBorder, c.height) + rect(c.width-cutBorder, 0, cutBorder, c.height)
	flash := shape(0, 0, colourOrange, alphaOpaque, border)

	firsts := make([]int, 0, len(t.video.Shots)-1)
	for _, s := range t.video.Shots[1:] {
		firsts = append(firsts, s.FirstFrame)
	}

	writeTrack(w, clk, track{layer: layerTop, text: func(i int) string {
		j, found := slices.BinarySearch(firsts, i)
		if found || (j > 0 && i-firsts[j-1] < cutFlash) {
			return flash
		}

		return ""
	}})
}
