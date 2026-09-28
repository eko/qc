package overlay

import (
	"bufio"
	"fmt"
	"strings"
)

// Style names of the script: plain text (and drawings), and badges drawn
// on an opaque box.
const (
	styleText  = "qc"
	styleBadge = "badge"
)

// Colours in ASS notation (&HBBGGRR&): the brand's orange, a muted grey
// for labels, and the traffic-light colours of quality values. The badges
// are the brand's ink on orange (styleBadge).
const (
	colourWhite  = "&HFFFFFF&"
	colourOrange = "&H439FFF&"
	colourMuted  = "&HA8B0B8&"
	colourGreen  = "&H80D34A&"
	colourAmber  = "&H24BFFB&"
	colourRed    = "&H5A5AF8&"
	colourBlack  = "&H000000&"
)

// Alpha levels (00 opaque, FF transparent) of the panels, the dim bars of
// the timeline and invisible fills.
const (
	alphaOpaque = "&H00&"
	alphaPanel  = "&H50&"
	alphaDim    = "&H90&"
	alphaHidden = "&HFF&"
)

// Font sizes, in canvas units (the canvas is canvasHeight tall).
const (
	fontText    = 26
	fontBig     = 36
	fontScore   = 56
	fontCaption = 20
	fontBadge   = 24
)

// writeHeader writes the script info and the styles. The canvas is the
// script's resolution: libass scales it to the video, so the overlay has
// the same proportions whatever the output height.
func writeHeader(
	w *bufio.Writer,
	c canvas,
	font string,
) {
	fmt.Fprintf(w, "[Script Info]\n"+
		"; Debug overlay written by qc (github.com/eko/qc)\n"+
		"ScriptType: v4.00+\n"+
		"PlayResX: %d\n"+
		"PlayResY: %d\n"+
		"ScaledBorderAndShadow: yes\n"+
		"WrapStyle: 2\n\n", c.width, c.height)

	font = strings.NewReplacer(",", " ", "\n", " ").Replace(font)

	fmt.Fprint(w, "[V4+ Styles]\n"+
		"Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, "+
		"Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, "+
		"Alignment, MarginL, MarginR, MarginV, Encoding\n")
	fmt.Fprintf(w, "Style: %s,%s,%d,&H00FFFFFF,&H00FFFFFF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,0,0,7,0,0,0,1\n",
		styleText, font, fontText)
	fmt.Fprintf(w, "Style: %s,%s,%d,&H0008101A,&H0008101A,&H00439FFF,&H00439FFF,-1,0,0,0,100,100,0,0,3,5,0,7,0,0,0,1\n\n",
		styleBadge, font, fontBadge)

	fmt.Fprint(w, "[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")
}

// Layers of the events: panels under text and drawings.
const (
	layerPanel = iota
	layerContent
	layerTop
)

// event is one line of the script.
type event struct {
	layer      int
	start, end centis
	style      string
	text       string
}

// write writes e as a Dialogue line.
func (e event) write(
	w *bufio.Writer,
) {
	fmt.Fprintf(w, "Dialogue: %d,%s,%s,%s,,0,0,0,,%s\n", e.layer, e.start, e.end, e.style, e.text)
}

// writeStatic writes an event shown for the whole title.
func writeStatic(
	w *bufio.Writer,
	clk clock,
	layer int,
	text string,
) {
	event{layer: layer, start: 0, end: clk.end(), style: styleText, text: text}.write(w)
}

// track is a part of the overlay that changes from frame to frame: text
// returns what it shows on frame i ("" for nothing).
type track struct {
	layer int
	style string
	text  func(i int) string
}

// writeTrack coalesces a track into events: one per run of frames showing
// the same thing, so that a static value costs one event however long it
// lasts. Runs shorter than a centisecond (frame rates of 100 fps and more)
// are dropped: ASS cannot time them.
func writeTrack(
	w *bufio.Writer,
	clk clock,
	t track,
) {
	style := t.style
	if style == "" {
		style = styleText
	}

	first, current := 0, t.text(0)

	for i := 1; i <= clk.frames(); i++ {
		next := ""
		if i < clk.frames() {
			next = t.text(i)
			if next == current {
				continue
			}
		}

		start, end := clk.start(first), clk.start(i)
		if current != "" && end > start {
			event{layer: t.layer, start: start, end: end, style: style, text: current}.write(w)
		}

		first, current = i, next
	}
}

// escape makes s literal ASS text: braces would open override blocks, and
// a backslash before n, N or h a line break or a hard space (a word joiner
// after it keeps it a plain backslash). Line breaks become spaces.
func escape(
	s string,
) string {
	return strings.NewReplacer(
		`\`, "\\\u2060",
		"{", `\{`,
		"}", `\}`,
		"\r\n", " ",
		"\n", " ",
		"\r", " ",
	).Replace(s)
}

// at positions an event by its top-left corner.
func at(
	x, y int,
) string {
	return fmt.Sprintf(`\an7\pos(%d,%d)`, x, y)
}

// shape is a filled vector drawing in colour and alpha, positioned at
// (x, y), of path (ASS drawing commands relative to that point).
func shape(
	x, y int,
	colour, alpha, path string,
) string {
	return fmt.Sprintf(`{%s\bord0\shad0\1c%s\1a%s\p1}%s{\p0}`, at(x, y), colour, alpha, path)
}

// rect is the drawing path of a w×h rectangle at (x, y).
func rect(
	x, y, w, h int,
) string {
	return fmt.Sprintf("m %d %d l %d %d %d %d %d %d ", x, y, x+w, y, x+w, y+h, x, y+h)
}

// colour switches the text colour.
func colour(
	c string,
) string {
	return `{\c` + c + `}`
}
