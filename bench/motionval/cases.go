package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/eko/qc/analyze/motion"
)

// Output geometry of the synthetic clips.
const (
	clipWidth  = 1920
	clipHeight = 1080
)

// sine is one component of a synthetic shake, in output pixels.
type sine struct {
	amplitude, frequency, phase float64
}

// path is a synthetic camera: the centre of the rendered window on the
// texture (texture pixels) and its magnification (1: one texture pixel per
// output pixel), as functions of time.
type path struct {
	// cx, cy is the start centre; vx, vy the speed in texture pixels per
	// second (output pixels at magnification 1).
	cx, cy, vx, vy float64
	// zoom is the magnification's growth rate (natural log per second).
	zoom float64
	// shake is added to the centre, horizontally (x) and vertically (y).
	shakeX, shakeY []sine
}

// at is the window centre and magnification at t seconds.
func (p path) at(
	t float64,
) (x, y, m float64) {
	x, y = p.cx+p.vx*t, p.cy+p.vy*t
	for _, s := range p.shakeX {
		x += s.amplitude * math.Sin(2*math.Pi*s.frequency*t+s.phase)
	}

	for _, s := range p.shakeY {
		y += s.amplitude * math.Sin(2*math.Pi*s.frequency*t+s.phase)
	}

	return x, y, math.Exp(p.zoom * t)
}

// expressions are the ffmpeg expressions of the centre and magnification,
// in the time expression t.
func (p path) expressions(
	t string,
) (x, y, m string) {
	sines := func(list []sine) string {
		var b strings.Builder
		for _, s := range list {
			fmt.Fprintf(&b, "+%g*sin(2*PI*%g*%s+%g)", s.amplitude, s.frequency, t, s.phase)
		}

		return b.String()
	}

	return fmt.Sprintf("%g+%g*%s%s", p.cx, p.vx, t, sines(p.shakeX)),
		fmt.Sprintf("%g+%g*%s%s", p.cy, p.vy, t, sines(p.shakeY)),
		fmt.Sprintf("exp(%g*%s)", p.zoom, t)
}

// clip is a synthetic shot (or two, split by a cut) with known camera
// motion.
type clip struct {
	name   string
	fps    int
	frames int
	path   path
	// cut, when positive, jumps to second at that time (seconds).
	cut    float64
	second path
	// post filters the rendered frames (noise, blur, fade); overlay
	// composes a moving layer over them (a label of layer).
	post  string
	layer layer
	// want is the expected class of each shot; shaky whether the first
	// shot is shaky.
	want  []expectation
	notes string
}

// expectation is the expected classification of a shot. Accept lists
// other classes counted as right (a static shot whose moving object is
// flagged as mixed, a noisy clip left unknown).
type expectation struct {
	class     motion.Class
	direction motion.Direction
	shaky     bool
	accept    []motion.Class
}

// layer is a textured rectangle moving over the picture: a moving object,
// or a foreground plane moving faster than the background (parallax).
type layer struct {
	// sx, sy, w, h is the rectangle on the texture; x, y its start on the
	// output, vx its horizontal speed (output pixels per second); scroll
	// scrolls its content horizontally instead of moving it.
	sx, sy, w, h, x, y, vx float64
	scroll                 bool
}

// cameraAt is the camera at frame n: its position (x, y) in output pixels
// of the frame's own magnification and the magnification m.
func (c clip) cameraAt(
	n int,
) (x, y, m float64) {
	t := float64(n) / float64(c.fps)
	if c.cut > 0 && t >= c.cut {
		return c.second.at(t)
	}

	return c.path.at(t)
}

// hand is a handheld shake: three incommensurate tremor components, amp
// output pixels each, in both directions.
func hand(
	amp float64,
) ([]sine, []sine) {
	return []sine{{amp, 2.3, 0}, {amp * 0.7, 4.1, 1}, {amp * 0.5, 7.3, 2}},
		[]sine{{amp * 0.8, 1.9, 0.5}, {amp * 0.6, 3.7, 2.5}, {amp * 0.4, 6.1, 1.3}}
}

// Texture anchors: the centre of the texture and a start leaving room for
// long moves right and down.
const (
	midX, midY     = 2748, 1545
	leftX, topY    = 1100, 700
	percentOfWidth = clipWidth / 100.0
	// defaultMinSpeed is the analyzer's pan threshold (% of width/s).
	defaultMinSpeed = 2.5
)

// clips are the validation cases.
func clips() []clip {
	out := []clip{
		{name: "static", fps: 25, frames: 75, path: path{cx: midX, cy: midY}, want: []expectation{{class: motion.ClassStatic}}},
		{name: "static-grain", fps: 25, frames: 75, path: path{cx: midX, cy: midY}, post: "noise=alls=24:allf=t",
			want: []expectation{{class: motion.ClassStatic}}, notes: "strong temporal grain"},
	}

	out = append(out, panSweep()...)
	out = append(out, zoomSweep()...)
	out = append(out, hardClips()...)

	return out
}

// panSweep pans and tilts at speeds from barely visible to whip pans, in
// % of the width per second.
func panSweep() []clip {
	var out []clip

	for _, speed := range []float64{1, 3, 5, 10, 25, 50, 100} {
		want := expectation{class: motion.ClassPan, direction: motion.DirectionRight}
		if speed < defaultMinSpeed {
			// A drift crossing the frame in 100 s is not a move.
			want = expectation{class: motion.ClassStatic}
		}

		frames := min(75, int(3300/(speed*percentOfWidth)*25))
		out = append(out,
			clip{name: fmt.Sprintf("pan-right-%g", speed), fps: 25, frames: frames,
				path: path{cx: leftX, cy: midY, vx: speed * percentOfWidth}, want: []expectation{want}})
	}

	for _, speed := range []float64{5, 20} {
		out = append(out,
			clip{name: fmt.Sprintf("pan-left-%g", speed), fps: 25, frames: 75,
				path: path{cx: 5496 - leftX, cy: midY, vx: -speed * percentOfWidth},
				want: []expectation{{class: motion.ClassPan, direction: motion.DirectionLeft}}},
			clip{name: fmt.Sprintf("tilt-down-%g", speed), fps: 25, frames: 75,
				path: path{cx: midX, cy: topY, vy: speed * percentOfWidth},
				want: []expectation{{class: motion.ClassTilt, direction: motion.DirectionDown}}},
			clip{name: fmt.Sprintf("tilt-up-%g", speed), fps: 25, frames: 75,
				path: path{cx: midX, cy: 3091 - topY, vy: -speed * percentOfWidth},
				want: []expectation{{class: motion.ClassTilt, direction: motion.DirectionUp}}})
	}

	for _, fps := range []int{24, 50} {
		out = append(out, clip{name: fmt.Sprintf("pan-right-10-%dfps", fps), fps: fps, frames: 3 * fps,
			path: path{cx: leftX, cy: midY, vx: 10 * percentOfWidth},
			want: []expectation{{class: motion.ClassPan, direction: motion.DirectionRight}}})
	}

	return out
}

// zoomSweep zooms in and out at several rates (% of scale per second).
func zoomSweep() []clip {
	var out []clip

	for _, rate := range []float64{3, 10, 25} {
		out = append(out,
			clip{name: fmt.Sprintf("zoom-in-%g", rate), fps: 25, frames: 75, path: path{cx: midX, cy: midY, zoom: rate / 100},
				want: []expectation{{class: motion.ClassZoom, direction: motion.DirectionIn}}},
			clip{name: fmt.Sprintf("zoom-out-%g", rate), fps: 25, frames: 75, path: path{cx: midX, cy: midY, zoom: -rate / 100},
				want: []expectation{{class: motion.ClassZoom, direction: motion.DirectionOut}}})
	}

	return out
}

// hardClips are the difficult cases: shake, combined moves, moving objects,
// parallax, poor pictures and cuts.
func hardClips() []clip {
	shakeX, shakeY := hand(6)
	lightX, lightY := hand(1.5)

	return []clip{
		{name: "handheld", fps: 25, frames: 100, path: path{cx: midX, cy: midY, shakeX: shakeX, shakeY: shakeY},
			want: []expectation{{class: motion.ClassHandheld, shaky: true}}, notes: "tremor 2-7 Hz, ±6 px per component"},
		{name: "handheld-light", fps: 25, frames: 100, path: path{cx: midX, cy: midY, shakeX: lightX, shakeY: lightY},
			want: []expectation{{class: motion.ClassStatic}}, notes: "tremor ±1.5 px: below the shaky threshold"},
		{name: "pan-shaky", fps: 25, frames: 75, path: path{cx: leftX, cy: midY, vx: 10 * percentOfWidth, shakeX: shakeX, shakeY: shakeY},
			want: []expectation{{class: motion.ClassPan, direction: motion.DirectionRight, shaky: true}}},
		{name: "pan-tilt", fps: 25, frames: 75, path: path{cx: leftX, cy: topY, vx: 10 * percentOfWidth, vy: 10 * percentOfWidth},
			want: []expectation{{class: motion.ClassMixed}}, notes: "diagonal move"},
		{name: "pan-zoom", fps: 25, frames: 75, path: path{cx: leftX, cy: midY, vx: 10 * percentOfWidth, zoom: 0.1},
			want: []expectation{{class: motion.ClassMixed}}},
		{name: "object-small", fps: 25, frames: 75, path: path{cx: midX, cy: midY},
			layer: layer{sx: 300, sy: 2400, w: 480, h: 360, x: 100, y: 500, vx: 450},
			want:  []expectation{{class: motion.ClassStatic}}, notes: "8% of the picture moving across a static shot"},
		{name: "object-large", fps: 25, frames: 75, path: path{cx: midX, cy: midY},
			layer: layer{sx: 300, sy: 2000, w: 900, h: 800, x: 50, y: 150, vx: 300},
			want:  []expectation{{class: motion.ClassStatic, accept: []motion.Class{motion.ClassMixed, motion.ClassUnknown}}}, notes: "35% of the picture moving"},
		{name: "parallax", fps: 25, frames: 75, path: path{cx: leftX, cy: 1300, vx: 5 * percentOfWidth},
			layer: layer{sx: 0, sy: 2600, w: 1920, h: 430, x: 0, y: 650, vx: 3 * 5 * percentOfWidth, scroll: true},
			want:  []expectation{{class: motion.ClassTracking, direction: motion.DirectionRight}}, notes: "lower 40% of the picture moves 3× faster than the background"},
		{name: "pan-low-texture", fps: 25, frames: 75, path: path{cx: leftX, cy: midY, vx: 10 * percentOfWidth}, post: "gblur=sigma=12",
			want: []expectation{{class: motion.ClassPan, direction: motion.DirectionRight, accept: []motion.Class{motion.ClassUnknown}}}},
		{name: "pan-noise", fps: 25, frames: 75, path: path{cx: leftX, cy: midY, vx: 10 * percentOfWidth}, post: "noise=alls=40:allf=t",
			want: []expectation{{class: motion.ClassPan, direction: motion.DirectionRight}}},
		{name: "static-fades", fps: 25, frames: 100, path: path{cx: midX, cy: midY}, post: "fade=t=in:st=0:d=1.5,fade=t=out:st=2.5:d=1.5",
			want: []expectation{{class: motion.ClassStatic}}, notes: "fade in and out, 1.5 s each"},
		{name: "cut", fps: 25, frames: 100, path: path{cx: leftX, cy: midY, vx: 10 * percentOfWidth}, cut: 2,
			second: path{cx: midX + 800, cy: topY + 400},
			want:   []expectation{{class: motion.ClassPan, direction: motion.DirectionRight}, {class: motion.ClassStatic}}},
	}
}
