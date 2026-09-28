package overlay

import (
	"bufio"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/eko/qc/analyze/motion"
)

// Camera move thresholds, in % of the width per second: under still, the
// frame's move shows as a dot; the vector grows through three lengths at
// the next thresholds (a slow pan is a few %/s, a whip pan over 30).
const (
	moveStill  = 0.5
	moveMedium = 5
	moveFast   = 15
)

// Lengths of the camera vector at the three speeds, in canvas units.
const (
	vectorSlow   = 14
	vectorMedium = 19
	vectorFast   = 24
)

// Geometry of the camera dial, in canvas units: its radius, the width of
// the vector's shaft, the length and width of its head, and the angle
// steps it turns by (coarse steps keep the dial still between frames).
const (
	dialRadius = 26
	shaftWidth = 4
	headLength = 9
	headWidth  = 14
	angleStep  = 15
)

// hasMotion reports whether the title has a camera motion analysis.
func (t *title) hasMotion() bool {
	return t.frames != nil && len(t.frames.MotionPan) > 0 && t.video != nil && len(t.video.Shots) > 0
}

// camera is the camera work of the shot of frame i, nil when unknown.
func (t *title) camera(
	i int,
) *motion.Shot {
	if s := t.shot[i]; s >= 0 {
		return t.video.Shots[s].Camera
	}

	return nil
}

// cameraRow is the camera work of the shot: its class, direction and
// whether the camera shakes.
func (t *title) cameraRow(
	i int,
) string {
	cam := t.camera(i)
	if cam == nil {
		return label("CAM") + "–"
	}

	text := label("CAM") + string(cam.Class)
	if cam.Direction != "" {
		text += " " + string(cam.Direction)
	}

	if cam.Shaky {
		text += "  " + colour(colourRed) + `{\b1}SHAKY{\b0}`
	}

	return text
}

// move is the camera move of frame i: pan and tilt (% of the width per
// second, positive right and up) and zoom (%/s, positive in), and whether
// the frame has an estimate.
func (t *title) move(
	i int,
) (pan, tilt, zoom float64, ok bool) {
	f := t.frames
	if conf, has := column(f.MotionConfidence, i); !has || conf <= 0 {
		return 0, 0, 0, false
	}

	pan, _ = column(f.MotionPan, i)
	tilt, _ = column(f.MotionTilt, i)
	zoom, _ = column(f.MotionZoom, i)

	return pan, tilt, zoom, true
}

// moveRow is the speed of the frame's camera move (its direction is on the
// dial) and its zoom, in %/s.
func (t *title) moveRow(
	i int,
) string {
	pan, tilt, zoom, ok := t.move(i)
	if !ok {
		return label("MOVE") + "–"
	}

	speed := strconv.FormatFloat(math.Hypot(pan, tilt), 'f', 1, 64)

	return label("MOVE") + speed + colour(colourMuted) + " %/s  ZOOM " + colour(colourWhite) + signed(zoom)
}

// writeDial writes the camera dial centred on (x, y): a ring for the whole
// title, and the vector of each frame's camera move.
func (t *title) writeDial(
	w *bufio.Writer,
	clk clock,
	x, y int,
) {
	ring := fmt.Sprintf(`{%s\bord2\shad0\3c%s\1a%s\p1}%s{\p0}`, at(x, y), colourMuted, alphaHidden, circle(dialRadius))
	writeStatic(w, clk, layerContent, ring)

	writeTrack(w, clk, track{layer: layerTop, text: func(i int) string {
		pan, tilt, _, ok := t.move(i)
		if !ok {
			return ""
		}

		return shape(x, y, colourOrange, alphaOpaque, vector(pan, tilt))
	}})
}

// circle is the path of a circle of radius r centred on the origin, as four
// cubic Béziers.
func circle(
	r int,
) string {
	// k places the control points of a quarter circle.
	k := int(math.Round(float64(r) * 0.5523))

	return fmt.Sprintf("m %d 0 b %d %d %d %d 0 %d b %d %d %d %d %d 0 b %d %d %d %d 0 %d b %d %d %d %d %d 0",
		r, r, k, k, r, r, -k, r, -r, k, -r, -r, -k, -k, -r, -r, k, -r, r, -k, r)
}

// vector is the path of the camera move's arrow from the origin: a dot for
// a still camera, otherwise an arrow in the move's direction (quantised to
// angleStep) whose length grows with its speed.
func vector(
	pan, tilt float64,
) string {
	speed := math.Hypot(pan, tilt)
	if speed < moveStill {
		return rect(-3, -3, 6, 6)
	}

	length := float64(vectorSlow)
	switch {
	case speed >= moveFast:
		length = vectorFast
	case speed >= moveMedium:
		length = vectorMedium
	}

	step := angleStep * math.Pi / 180
	angle := math.Round(math.Atan2(tilt, pan)/step) * step

	return arrow(length, angle)
}

// arrow is the path of an arrow of length from the origin towards angle
// (radians, counterclockwise from the right; the screen's y axis points
// down).
func arrow(
	length, angle float64,
) string {
	neck := max(length-headLength, 0)
	// Outline of an arrow pointing right: shaft then head.
	outline := [][2]float64{
		{0, -shaftWidth / 2}, {neck, -shaftWidth / 2}, {neck, -headWidth / 2}, {length, 0},
		{neck, headWidth / 2}, {neck, shaftWidth / 2}, {0, shaftWidth / 2},
	}

	cos, sin := math.Cos(angle), math.Sin(angle)

	var b strings.Builder

	for j, p := range outline {
		x := p[0]*cos - p[1]*sin
		y := -(p[0]*sin + p[1]*cos)

		switch j {
		case 0:
			b.WriteString("m ")
		case 1:
			b.WriteString("l ")
		}

		fmt.Fprintf(&b, "%s %s ", integer(x), integer(y))
	}

	return b.String()
}
