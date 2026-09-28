package motion

import (
	"cmp"
	"math"
	"math/cmplx"
	"slices"

	"github.com/eko/qc/analyze/scene"
	"github.com/eko/qc/media"
)

// Class is the camera work of a shot.
type Class string

// Classes of camera work.
const (
	// ClassStatic: the camera does not move (subjects may).
	ClassStatic Class = "static"
	// ClassPan and ClassTilt: the camera turns horizontally or vertically.
	ClassPan  Class = "pan"
	ClassTilt Class = "tilt"
	// ClassZoom: the picture scales about its centre (a zoom, or a dolly
	// move without visible parallax).
	ClassZoom Class = "zoom"
	// ClassTracking: the camera travels (tracking, dolly, crane): the
	// motion is not one similarity, depth layers move at different speeds.
	ClassTracking Class = "tracking"
	// ClassHandheld: no steady move, but a shaky camera path.
	ClassHandheld Class = "handheld"
	// ClassMixed: several moves (pan then tilt, pan and zoom, back and
	// forth).
	ClassMixed Class = "mixed"
	// ClassUnknown: too few reliable frames (flat pictures, fades, very
	// short shots).
	ClassUnknown Class = "unknown"
)

// Direction is the direction of the camera (not of the picture content):
// a camera panning right moves the content left.
type Direction string

// Directions of camera moves.
const (
	DirectionLeft  Direction = "left"
	DirectionRight Direction = "right"
	DirectionUp    Direction = "up"
	DirectionDown  Direction = "down"
	DirectionIn    Direction = "in"
	DirectionOut   Direction = "out"
)

// Series holds per-frame camera motion as columns, one value per frame.
// Pan, Tilt, Zoom and Roll are the low-passed camera move; frames without
// a reliable estimate (Confidence 0, e.g. the first frame of a shot) hold
// values interpolated from their neighbours in the shot.
type Series struct {
	// Pan and Tilt are in % of the picture width per second, positive
	// when the camera turns right (up).
	Pan  []float64
	Tilt []float64
	// Zoom is in % of scale per second, positive when zooming in.
	Zoom []float64
	// Roll is in degrees per second, positive when the camera rolls
	// clockwise.
	Roll []float64
	// Shake is the jitter of the camera path around its low-passed
	// trajectory, in % of the picture width.
	Shake []float64
	// Confidence is the confidence of each frame's estimate, 0 to 1.
	Confidence []float64
}

// Shot is the camera work of one shot.
type Shot struct {
	Class     Class     `json:"class"`
	Direction Direction `json:"direction,omitempty"`
	// Shaky flags a shot whose camera path jitters (shake above
	// Options.MaxShake), whatever its move.
	Shaky bool `json:"shaky,omitempty"`
	// Confidence (0-1) combines the share of reliable frames, their
	// confidence and how clearly one class dominates.
	Confidence float64 `json:"confidence"`
	// Pan, Tilt (% of width/s), Zoom (%/s) and Roll (°/s) are the mean
	// camera move over the reliable frames, signed as in Series.
	Pan  float64 `json:"pan"`
	Tilt float64 `json:"tilt"`
	Zoom float64 `json:"zoom"`
	Roll float64 `json:"roll"`
	// Moving is the share of reliable frames where the camera moves.
	Moving float64 `json:"moving"`
	// Shake is the median jitter of the camera path over the reliable
	// frames, in % of the width.
	Shake float64 `json:"shake"`
	// Parallax is the misfit of the global model relative to the motion
	// on moving frames (median): high for tracking shots.
	Parallax float64 `json:"parallax"`
}

// ClassShare is the part of a title a class of camera work covers.
type ClassShare struct {
	Class    Class          `json:"class"`
	Shots    int            `json:"shots"`
	Duration media.Duration `json:"duration"`
	// Share is Duration over the title's duration.
	Share float64 `json:"share"`
}

// Summary sums the camera work of a title up.
type Summary struct {
	// Classes are the classes present, longest first.
	Classes []ClassShare `json:"classes"`
	// ShakyShots counts the shots flagged Shaky.
	ShakyShots int `json:"shakyShots"`
	// Reliable is the share of frames with a reliable estimate.
	Reliable float64 `json:"reliable"`
}

// Result is the camera motion of a title.
type Result struct {
	Frames  Series
	Shots   []Shot
	Summary Summary
}

// Result classifies the camera work of every shot. shots come from the
// scene detector, over the same frames: the first frame of a shot is
// compared with the previous shot's last one, so its estimate is ignored.
func (a *Analyzer) Result(
	shots []scene.Shot,
) Result {
	samples := a.series.Merge()
	width := float64(a.width.Load())
	n := len(samples)
	out := Result{Frames: newSeries(n), Shots: make([]Shot, len(shots))}
	pts := make([]media.Duration, n)

	for i, s := range samples {
		pts[i] = s.pts
	}

	dt := frameDurations(pts)
	reliable := 0

	for i, s := range shots {
		first, last := s.FirstFrame, min(s.LastFrame, n-1)
		if width == 0 || last < first {
			out.Shots[i] = Shot{Class: ClassUnknown}

			continue
		}

		t := newTrack(samples[first:last+1], dt[first:last+1], width, a.opts)
		reliable += t.reliable
		out.Shots[i] = t.classify(a.opts)
		t.store(&out.Frames, first)
	}

	out.Summary = summarize(out.Shots, shots, float64(reliable)/float64(max(n, 1)))

	return out
}

func newSeries(
	n int,
) Series {
	return Series{
		Pan: make([]float64, n), Tilt: make([]float64, n), Zoom: make([]float64, n),
		Roll: make([]float64, n), Shake: make([]float64, n), Confidence: make([]float64, n),
	}
}

// fallbackFrameDuration is used when timestamps say nothing (25 fps).
const fallbackFrameDuration = 0.04

// frameDurations is the time from the previous frame to each frame, in
// seconds; the first frame, and frames with no positive interval, get the
// median interval.
func frameDurations(
	pts []media.Duration,
) []float64 {
	dt := make([]float64, len(pts))

	var positive []float64

	for i := 1; i < len(pts); i++ {
		if d := (pts[i] - pts[i-1]).Seconds(); d > 0 {
			dt[i] = d
			positive = append(positive, d)
		}
	}

	nominal := fallbackFrameDuration
	if len(positive) > 0 {
		slices.Sort(positive)
		nominal = positive[len(positive)/2]
	}

	for i, d := range dt {
		if d <= 0 {
			dt[i] = nominal
		}
	}

	return dt
}

// summarize adds up the shots' classes by duration.
func summarize(
	classes []Shot,
	shots []scene.Shot,
	reliable float64,
) Summary {
	var (
		total  media.Duration
		shares []ClassShare
		shaky  int
	)

	for i, s := range shots {
		length := s.Length()
		total += length

		if classes[i].Shaky {
			shaky++
		}

		j := slices.IndexFunc(shares, func(c ClassShare) bool { return c.Class == classes[i].Class })
		if j < 0 {
			shares, j = append(shares, ClassShare{Class: classes[i].Class}), len(shares)
		}

		shares[j].Shots++
		shares[j].Duration += length
	}

	for i := range shares {
		if total > 0 {
			shares[i].Share = shares[i].Duration.Seconds() / total.Seconds()
		}
	}

	slices.SortStableFunc(shares, func(a, b ClassShare) int { return cmp.Compare(b.Duration, a.Duration) })

	return Summary{Classes: shares, ShakyShots: shaky, Reliable: reliable}
}

// cameraRates converts a per-frame model into camera rates: pan and tilt in
// % of the width per second, zoom in % per second, roll in degrees per
// second. The camera moves opposite to the content horizontally (panning
// right moves the content left) and with it vertically in image
// coordinates (y down: tilting up moves the content down).
func cameraRates(
	m similarity,
	dt, width float64,
) (pan, tilt, zoom, roll float64) {
	const percent, degrees = 100, 180 / math.Pi

	scale := 1 + m.z

	return -real(m.t) / width * percent / dt,
		imag(m.t) / width * percent / dt,
		math.Log(cmplx.Abs(scale)) * percent / dt,
		-cmplx.Phase(scale) * degrees / dt
}
